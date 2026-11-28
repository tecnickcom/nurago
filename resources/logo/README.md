# nurago logo

`nurago-logo.svg` is the nurago logo: a single colour (`#00ADD8`) on a transparent background.

`nurago-logo.png` is a 512x512 rendering of the SVG for tools that do not accept SVG files, with the copyright notice embedded as PNG text. It is generated with:

```bash
rsvg-convert -w 512 -h 512 nurago-logo.svg | magick - \
  -set Title "nurago" \
  -set Description "Logo of nurago, a collection of independent Go packages for building backend services: https://nurago.org" \
  -set Author "Nicola Asuni - Tecnick.com LTD" \
  -set Copyright "Copyright (c) 2026 Nicola Asuni - Tecnick.com LTD. All rights reserved. This logo is not covered by the nurago MIT license. It may be used unmodified to refer to the nurago project." \
  -set Source "https://github.com/tecnickcom/nurago" \
  -define png:exclude-chunks=date,time \
  nurago-logo.png
```

## Usage

Copyright (c) 2026 Nicola Asuni - Tecnick.com LTD. All rights reserved.

The nurago logo is not covered by the MIT license that applies to the rest of this repository.

The logo may be used unmodified to refer to the nurago project, for example in articles, talks, and "built with nurago" badges. Any other use, including modified versions or use as the logo of another project or product, requires prior written permission.
