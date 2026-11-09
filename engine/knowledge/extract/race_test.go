//go:build race

package extract

// The race detector makes the parsers several times slower.
func init() { timeScale = 10 }
