# Installs captures as the site's shots: each at full size, and beside it an
# exact half-size copy named for its width (deck-1584.png), every 2x2 block of
# pixels averaged (Image.reduce is a box filter). A capture must be the size
# of the shot it replaces, since the templates' width, height and srcset say
# so.
#
#   python halve.py SHOTS_DIR capture.png=name ...
import os
import sys

from PIL import Image

shots, pairs = sys.argv[1], sys.argv[2:]
for pair in pairs:
    src, name = pair.rsplit("=", 1)
    im = Image.open(src).convert("RGB")
    dest = os.path.join(shots, name + ".png")
    if os.path.exists(dest):
        want = Image.open(dest).size
        if im.size != want:
            sys.exit(f"{src} is {im.size}, but {name}.png is {want}")
    w, h = im.size
    assert w % 2 == 0 and h % 2 == 0, (src, im.size)
    half = im.reduce(2)
    im.save(dest, optimize=True)
    half.save(os.path.join(shots, f"{name}-{w // 2}.png"), optimize=True)
    print(name, im.size, half.size)
