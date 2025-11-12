//go:build ignore
// +build ignore

package main

import (
	"image"
	"image/color"
	"image/png"
	"os"
)

func main() {
	// Create simple 22x22 icons for menu bar
	size := 22

	// Stopped icon - gray circle
	createIcon("icon.png", size, color.RGBA{128, 128, 128, 255})

	// Starting icon - yellow circle
	createIcon("icon-starting.png", size, color.RGBA{255, 200, 0, 255})

	// Running icon - green circle
	createIcon("icon-running.png", size, color.RGBA{76, 175, 80, 255})

	// Stopping icon - orange circle
	createIcon("icon-stopping.png", size, color.RGBA{255, 152, 0, 255})

	// Error icon - red circle
	createIcon("icon-error.png", size, color.RGBA{244, 67, 54, 255})
}

func createIcon(filename string, size int, c color.RGBA) {
	img := image.NewRGBA(image.Rect(0, 0, size, size))

	// Draw a simple circle
	centerX, centerY := size/2, size/2
	radius := size / 2

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx := x - centerX
			dy := y - centerY
			if dx*dx+dy*dy <= radius*radius {
				img.Set(x, y, c)
			} else {
				img.Set(x, y, color.Transparent)
			}
		}
	}

	f, err := os.Create(filename)
	if err != nil {
		panic(err)
	}
	defer f.Close()

	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
}
