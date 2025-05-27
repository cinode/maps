package geo

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

var (
	testBBoxPL = BBox{
		MinLat: 49.0061,
		MinLon: 14.1213,
		MaxLat: 54.8357,
		MaxLon: 24.1533,
	}

	testBBoxSF = BBox{
		MinLat: 37.6960,
		MinLon: -122.5600,
		MaxLat: 37.8190,
		MaxLon: -122.3470,
	}
)

func TestBBoxContainsTile(t *testing.T) {
	for i, d := range []struct {
		bBox     BBox
		x, y, z  int
		contains bool
	}{
		{testBBoxPL, 8, 5, 4, true},
		{testBBoxPL, 9, 5, 4, true},
		{testBBoxPL, 8, 4, 4, false},
		{testBBoxPL, 8, 6, 4, false},
		{testBBoxPL, 285, 168, 9, true},
	} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			require.Equal(t,
				d.contains,
				d.bBox.ContainsTile(d.x, d.y, d.z),
			)
		})
	}
}

func TestBBoxTilesRange(t *testing.T) {
	for i, d := range []struct {
		bBox                   BBox
		z                      int
		minX, minY, maxX, maxY int
	}{
		{testBBoxPL, 0, 0, 0, 0, 0},
		{testBBoxPL, 1, 1, 0, 1, 0},
		{testBBoxPL, 5, 17, 10, 18, 10},
		{testBBoxPL, 18, 141354, 83123, 148659, 90019},
		{testBBoxSF, 0, 0, 0, 0, 0},
		{testBBoxSF, 1, 0, 0, 0, 0},
		{testBBoxSF, 13, 1307, 3165, 1311, 3168},
		{testBBoxSF, 18, 41826, 101283, 41981, 101396},
	} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			minX, minY, maxX, maxY := d.bBox.GetTilesRange(d.z)
			require.Equal(t, d.minX, minX, "minX")
			require.Equal(t, d.minY, minY, "minY")
			require.Equal(t, d.maxX, maxX, "maxX")
			require.Equal(t, d.maxY, maxY, "maxY")
		})
	}
}
