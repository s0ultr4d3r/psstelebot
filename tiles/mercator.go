package tiles

import "math"

func MercX(lon float64) float64 { return (lon + 180.0) / 360.0 }
func MercY(lat float64) float64 {
	s := math.Sin(lat * math.Pi / 180.0)
	return 0.5 - math.Log((1+s)/(1-s))/(4*math.Pi)
}
