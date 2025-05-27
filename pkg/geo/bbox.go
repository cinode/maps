package geo

import "math"

type BBox struct {
	MinLat float64 `yaml:"minLat"`
	MinLon float64 `yaml:"minLon"`
	MaxLat float64 `yaml:"maxLat"`
	MaxLon float64 `yaml:"maxLon"`
}

func BBoxFromTile(x, y, z int) BBox {
	return BBox{
		MinLon: leftEdgeLon(x, z),
		MinLat: topEdgeLat(y+1, z),
		MaxLon: leftEdgeLon(x+1, z),
		MaxLat: topEdgeLat(y, z),
	}
}

func leftEdgeLon(x, z int) float64 {
	n := float64(int(1) << z)
	mapX := float64(x) / n
	return (mapX)*360.0 - 180.0
}

func topEdgeLat(y, z int) float64 {
	n := float64(int(1) << z)
	mapY := float64(y) / n
	rad := math.Atan(math.Sinh(math.Pi * (1 - 2*mapY)))
	return rad * 180.0 / math.Pi
}

func (b BBox) ContainsColumn(x, z int) bool {
	minLon := leftEdgeLon(x, z)
	maxLon := leftEdgeLon(x+1, z)
	return max(minLon, b.MinLon) <= min(maxLon, b.MaxLon)
}

func (b BBox) ContainsRow(y, z int) bool {
	minLat := topEdgeLat(y+1, z)
	maxLat := topEdgeLat(y, z)
	return max(minLat, b.MinLat) <= min(maxLat, b.MaxLat)
}

func (b BBox) ContainsTile(x, y, z int) bool {
	return b.ContainsColumn(x, z) && b.ContainsRow(y, z)
}

func tileX(lon float64, z int) int {
	n := float64(int(1) << z)
	return int(n * (lon + 180.0) / 360.0)
}

func tileY(lat float64, z int) int {
	n := float64(int(1) << z)
	latRad := lat * math.Pi / 180.0

	mercN := math.Log(math.Tan(math.Pi/4 + latRad/2))
	return int((n / 2) - (n * mercN / (2 * math.Pi)))
}

func (b BBox) GetTilesRange(z int) (minX, minY, maxX, maxY int) {
	return tileX(b.MinLon, z), tileY(b.MaxLat, z), tileX(b.MaxLon, z), tileY(b.MinLat, z)
}
