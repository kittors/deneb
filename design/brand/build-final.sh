#!/bin/sh
# build-final.sh: every file of Deneb's mark into final/, and the copies the
# app builds with (cmd/deneb: Deneb.icns, icon.png, icon-dev.png, trayicons/).
# The mark is drawn in c.py (the little star, chosen 2026-11-13); final_c.py
# renders it with headless Chrome. The second logo (2026-11-11) and its tools
# are kept in v2/, the first in v1/.
set -eu
cd "$(dirname "$0")"
python3 -I final_c.py
