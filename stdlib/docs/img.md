### [ img ] - Image Manipulation

All paths workspace-jailed. Supports JPEG, PNG, GIF.

[ Operations ]
* img.info(path: string) → {width, height, format}
  Get image dimensions and format.
* img.resize(src: string, dst: string, opts: {width?, height?}) → void
  Resize image. Omit one dimension to maintain aspect ratio.
* img.crop(src: string, dst: string, opts: {x, y, width, height}) → void
  Crop a region from the image.
* img.convert(src: string, dst: string) → void
  Convert format based on dst file extension (.png, .jpg, .gif).
* img.rotate(src: string, dst: string, degrees: number) → void
  Rotate image. Degrees must be 90, 180, or 270.
