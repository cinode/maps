// Client-side over-zooming of vector tiles.
//
// Tiles are pre-generated up to different max zoom levels in different
// regions (e.g. z6 for the whole world, z14 for detailed regions). MapLibre
// only supports a single max zoom per source, so for tiles that don't exist
// this module finds the closest existing ancestor tile and rescales its
// geometry so that it can be rendered as the requested tile.
//
// Only the geometry of MVT features is rewritten, all other protobuf fields
// are copied verbatim, so no vector-tile library is needed.

// ---- protobuf helpers ------------------------------------------------------

function readVarint(buf, pos) {
    let val = 0, mul = 1, b;
    do {
        b = buf[pos.p++];
        val += (b & 0x7f) * mul;
        mul *= 128;
    } while (b & 0x80);
    return val;
}

function writeVarint(out, val) {
    while (val >= 0x80) {
        out.push((val % 128) | 0x80);
        val = Math.floor(val / 128);
    }
    out.push(val);
}

const zigzagEncode = (n) => (n >= 0 ? 2 * n : -2 * n - 1);
const zigzagDecode = (n) => (n % 2 === 0 ? n / 2 : -(n + 1) / 2);

// Splits a message into a list of fields: {tag, wire, start, end, value}
// where start..end covers the whole field (key included) and value is
// a varint value or [from, to) payload range for length-delimited fields.
function readFields(buf, from, to) {
    const fields = [];
    const pos = { p: from };
    while (pos.p < to) {
        const start = pos.p;
        const key = readVarint(buf, pos);
        const tag = Math.floor(key / 8), wire = key & 7;
        let value;
        switch (wire) {
            case 0: value = readVarint(buf, pos); break;
            case 1: pos.p += 8; break;
            case 2: {
                const len = readVarint(buf, pos);
                value = [pos.p, pos.p + len];
                pos.p += len;
                break;
            }
            case 5: pos.p += 4; break;
            default: throw new Error(`unsupported wire type ${wire}`);
        }
        fields.push({ tag, wire, start, end: pos.p, value });
    }
    return fields;
}

function pushBytes(out, bytes) {
    for (let i = 0; i < bytes.length; i++) out.push(bytes[i]);
}

function pushLenDelimited(out, tag, bytes) {
    writeVarint(out, tag * 8 + 2);
    writeVarint(out, bytes.length);
    pushBytes(out, bytes);
}

// ---- MVT rewriting ---------------------------------------------------------

// Rescales geometry of a single feature, returns null if the feature lies
// completely outside of the target tile (including a buffer).
function transformGeometry(buf, [from, to], scale, offX, offY, extent) {
    const pos = { p: from };
    const out = [];
    let x = 0, y = 0;      // cursor in source tile coordinates
    let nx = 0, ny = 0;    // cursor in target tile coordinates
    let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;

    while (pos.p < to) {
        const cmdInt = readVarint(buf, pos);
        writeVarint(out, cmdInt);
        const cmd = cmdInt & 7, count = Math.floor(cmdInt / 8);
        if (cmd !== 1 && cmd !== 2) continue; // ClosePath has no params
        for (let i = 0; i < count; i++) {
            x += zigzagDecode(readVarint(buf, pos));
            y += zigzagDecode(readVarint(buf, pos));
            const tx = x * scale - offX, ty = y * scale - offY;
            writeVarint(out, zigzagEncode(tx - nx));
            writeVarint(out, zigzagEncode(ty - ny));
            nx = tx; ny = ty;
            if (tx < minX) minX = tx;
            if (tx > maxX) maxX = tx;
            if (ty < minY) minY = ty;
            if (ty > maxY) maxY = ty;
        }
    }

    const buffer = extent / 8;
    if (maxX < -buffer || maxY < -buffer || minX > extent + buffer || minY > extent + buffer) {
        return null;
    }
    return out;
}

function transformFeature(buf, [from, to], scale, ox, oy, extent) {
    const out = [];
    for (const f of readFields(buf, from, to)) {
        if (f.tag === 4 && f.wire === 2) {
            const geom = transformGeometry(buf, f.value, scale, ox * extent, oy * extent, extent);
            if (!geom) return null;
            pushLenDelimited(out, 4, geom);
        } else {
            pushBytes(out, buf.subarray(f.start, f.end));
        }
    }
    return out;
}

function transformLayer(buf, [from, to], scale, ox, oy) {
    const fields = readFields(buf, from, to);
    const extentField = fields.find((f) => f.tag === 5 && f.wire === 0);
    const extent = extentField ? extentField.value : 4096;

    const out = [];
    for (const f of fields) {
        if (f.tag === 2 && f.wire === 2) {
            const feature = transformFeature(buf, f.value, scale, ox, oy, extent);
            if (feature) pushLenDelimited(out, 2, feature);
        } else {
            pushBytes(out, buf.subarray(f.start, f.end));
        }
    }
    return out;
}

// Converts tile data of tile (pz, px, py) into tile (z, x, y), where the
// latter is a descendant of the former.
export function overzoomTile(data, pz, px, py, z, x, y) {
    const buf = new Uint8Array(data);
    const dz = z - pz;
    if (dz === 0) return buf;
    const scale = 2 ** dz;
    const ox = x - px * scale, oy = y - py * scale;

    const out = [];
    for (const f of readFields(buf, 0, buf.length)) {
        if (f.tag === 3 && f.wire === 2) {
            pushLenDelimited(out, 3, transformLayer(buf, f.value, scale, ox, oy));
        } else {
            pushBytes(out, buf.subarray(f.start, f.end));
        }
    }
    return new Uint8Array(out);
}

// ---- MapLibre protocol -----------------------------------------------------

// Registers `<name>://<url with {z}/{x}/{y}>` protocol in MapLibre.
// The tile URL itself is a regular http(s) URL with z/x/y already
// substituted by MapLibre, e.g. `overzoom://http://host/tiles/9/1/2.pbf`.
//
// If `coverage.json` exists next to the tiles (generated by map-toolbox),
// it is used to skip ancestors that certainly don't exist, otherwise
// missing tiles are found by probing (404 responses).
export function registerOverzoomProtocol(maplibregl, name = 'overzoom') {
    const missing = new Set();   // urls of tiles known not to exist
    const coverages = new Map(); // tiles url prefix -> Promise<coverage | null>
    const tileRE = /^(.*\/)(\d+)\/(\d+)\/(\d+)(\.[a-z]+)$/;

    const tileURL = (prefix, z, x, y, ext) => `${prefix}${z}/${x}/${y}${ext}`;

    const getCoverage = (prefix) => {
        if (!coverages.has(prefix)) {
            coverages.set(prefix, fetch(prefix + 'coverage.json')
                .then((r) => (r.ok ? r.json() : null))
                .then((c) => (c && c.zooms ? c.zooms : null))
                .catch(() => null));
        }
        return coverages.get(prefix);
    };

    const mayExist = (coverage, z, x, y) => {
        if (!coverage) return true;
        const rects = coverage[z];
        return !!rects && rects.some(([x0, y0, x1, y1]) => x >= x0 && x <= x1 && y >= y0 && y <= y1);
    };

    maplibregl.addProtocol(name, async (params, abortController) => {
        const url = params.url.slice(name.length + 3);
        const m = tileRE.exec(url);
        if (!m) throw new Error(`invalid tile url: ${url}`);
        const [, prefix, zs, xs, ys, ext] = m;
        const z = +zs, x = +xs, y = +ys;
        const coverage = await getCoverage(prefix);

        for (let pz = z; pz >= 0; pz--) {
            const px = x >> (z - pz), py = y >> (z - pz);
            if (!mayExist(coverage, pz, px, py)) continue;
            const u = tileURL(prefix, pz, px, py, ext);
            if (missing.has(u)) continue;

            const resp = await fetch(u, { signal: abortController.signal });
            if (resp.status === 404) {
                missing.add(u);
                continue;
            }
            if (!resp.ok) throw new Error(`tile fetch failed: ${resp.status} ${u}`);

            const data = await resp.arrayBuffer();
            return { data: overzoomTile(data, pz, px, py, z, x, y).buffer };
        }
        return { data: new ArrayBuffer(0) };
    });
}
