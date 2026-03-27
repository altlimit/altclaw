package bridge

import (
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/dop251/goja"
	"golang.org/x/image/draw"
)

// RegisterImage adds the image namespace to the runtime.
// All paths are workspace-jailed.
//
//	image.info(path)                          → {width, height, format}
//	image.resize(src, dst, {width, height})   → void
//	image.crop(src, dst, {x, y, width, height}) → void
//	image.convert(src, dst)                   → void (format from extension)
//	image.rotate(src, dst, degrees)           → void (90, 180, 270)
func RegisterImage(vm *goja.Runtime, workspace string) {
	imgObj := vm.NewObject()

	safe := func(op, path string) string {
		full, err := SanitizePath(workspace, path)
		if err != nil {
			Throwf(vm, "%s: %s", op, err)
		}
		return full
	}

	loadImg := func(op, path string) (image.Image, string) {
		f, err := os.Open(path)
		if err != nil {
			logErr(vm, op, err)
		}
		defer f.Close()
		img, format, err := image.Decode(f)
		if err != nil {
			logErr(vm, op, err)
		}
		return img, format
	}

	saveImg := func(op, path string, img image.Image) {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			logErr(vm, op, err)
		}
		f, err := os.Create(path)
		if err != nil {
			logErr(vm, op, err)
		}
		defer f.Close()

		ext := strings.ToLower(filepath.Ext(path))
		switch ext {
		case ".png":
			err = png.Encode(f, img)
		case ".gif":
			err = gif.Encode(f, img, nil)
		default: // .jpg, .jpeg, or anything else
			err = jpeg.Encode(f, img, &jpeg.Options{Quality: 90})
		}
		if err != nil {
			logErr(vm, op, err)
		}
	}

	// image.info(path) → {width, height, format}
	imgObj.Set("info", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "image.info requires a file path")
		}
		path := safe("image.info", call.Arguments[0].String())
		f, err := os.Open(path)
		if err != nil {
			logErr(vm, "image.info", err)
		}
		defer f.Close()

		cfg, format, err := image.DecodeConfig(f)
		if err != nil {
			logErr(vm, "image.info", err)
		}
		return vm.ToValue(map[string]interface{}{
			"width":  cfg.Width,
			"height": cfg.Height,
			"format": format,
		})
	})

	// image.resize(src, dst, {width, height})
	imgObj.Set("resize", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 3 {
			Throw(vm, "image.resize requires src, dst, and options {width, height}")
		}
		srcPath := safe("image.resize", call.Arguments[0].String())
		dstPath := safe("image.resize", call.Arguments[1].String())
		opts := call.Arguments[2].ToObject(vm)

		width := int(opts.Get("width").ToInteger())
		height := int(opts.Get("height").ToInteger())
		if width <= 0 && height <= 0 {
			Throw(vm, "image.resize: width or height must be > 0")
		}

		src, _ := loadImg("image.resize", srcPath)
		bounds := src.Bounds()

		// Maintain aspect ratio if only one dimension specified
		if width <= 0 {
			width = bounds.Dx() * height / bounds.Dy()
		}
		if height <= 0 {
			height = bounds.Dy() * width / bounds.Dx()
		}

		dst := image.NewRGBA(image.Rect(0, 0, width, height))
		draw.CatmullRom.Scale(dst, dst.Bounds(), src, bounds, draw.Over, nil)
		saveImg("image.resize", dstPath, dst)
		return goja.Undefined()
	})

	// image.crop(src, dst, {x, y, width, height})
	imgObj.Set("crop", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 3 {
			Throw(vm, "image.crop requires src, dst, and options {x, y, width, height}")
		}
		srcPath := safe("image.crop", call.Arguments[0].String())
		dstPath := safe("image.crop", call.Arguments[1].String())
		opts := call.Arguments[2].ToObject(vm)

		x := int(opts.Get("x").ToInteger())
		y := int(opts.Get("y").ToInteger())
		w := int(opts.Get("width").ToInteger())
		h := int(opts.Get("height").ToInteger())
		if w <= 0 || h <= 0 {
			Throw(vm, "image.crop: width and height must be > 0")
		}

		src, _ := loadImg("image.crop", srcPath)
		cropRect := image.Rect(x, y, x+w, y+h)

		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.Copy(dst, image.Point{}, src, cropRect, draw.Over, nil)
		saveImg("image.crop", dstPath, dst)
		return goja.Undefined()
	})

	// image.convert(src, dst) — format conversion based on dst extension
	imgObj.Set("convert", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			Throw(vm, "image.convert requires src and dst paths")
		}
		srcPath := safe("image.convert", call.Arguments[0].String())
		dstPath := safe("image.convert", call.Arguments[1].String())
		src, _ := loadImg("image.convert", srcPath)
		saveImg("image.convert", dstPath, src)
		return goja.Undefined()
	})

	// image.rotate(src, dst, degrees) — 90, 180, 270
	imgObj.Set("rotate", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 3 {
			Throw(vm, "image.rotate requires src, dst, and degrees (90, 180, 270)")
		}
		srcPath := safe("image.rotate", call.Arguments[0].String())
		dstPath := safe("image.rotate", call.Arguments[1].String())
		degrees := int(call.Arguments[2].ToInteger())

		src, _ := loadImg("image.rotate", srcPath)
		bounds := src.Bounds()
		w, h := bounds.Dx(), bounds.Dy()

		var dst *image.RGBA
		switch degrees {
		case 90:
			dst = image.NewRGBA(image.Rect(0, 0, h, w))
			for py := 0; py < h; py++ {
				for px := 0; px < w; px++ {
					dst.Set(h-1-py, px, src.At(px+bounds.Min.X, py+bounds.Min.Y))
				}
			}
		case 180:
			dst = image.NewRGBA(image.Rect(0, 0, w, h))
			for py := 0; py < h; py++ {
				for px := 0; px < w; px++ {
					dst.Set(w-1-px, h-1-py, src.At(px+bounds.Min.X, py+bounds.Min.Y))
				}
			}
		case 270:
			dst = image.NewRGBA(image.Rect(0, 0, h, w))
			for py := 0; py < h; py++ {
				for px := 0; px < w; px++ {
					dst.Set(py, w-1-px, src.At(px+bounds.Min.X, py+bounds.Min.Y))
				}
			}
		default:
			Throwf(vm, "image.rotate: unsupported angle %d (use 90, 180, 270)", degrees)
		}

		saveImg("image.rotate", dstPath, dst)
		return goja.Undefined()
	})

	vm.Set(NameImage, imgObj)
}

// Ensure format decoders are registered for image.Decode
var _ = fmt.Sprint // keep fmt imported
