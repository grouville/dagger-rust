#!/usr/bin/env python3
"""Apply the fork's benchmark edits to a ripgrep checkout.
usage: edit.py <root> app|lib|reset <n>"""
import sys, pathlib
root = pathlib.Path(sys.argv[1]); kind = sys.argv[2]; n = sys.argv[3] if len(sys.argv) > 3 else "0"
if kind == "file": n = sys.argv[4] if len(sys.argv) > 4 else "0"
app = root / "crates/core/flags/doc/version.rs"
lib = root / "crates/printer/src/standard.rs"
APP_BASE = 'format!("ripgrep {digits}")'
LIB_BASE = "[Omitted long context line]"
def restore(p, base_marker, tag):
    s = p.read_text()
    import re
    s = re.sub(re.escape(tag).replace(r"\{n\}", r"[^\"\]]*"), base_marker, s)
    p.write_text(s)
if kind == "app":
    s = app.read_text(); assert APP_BASE in s or "bench-" in s
    import re; s = re.sub(r'format!\("ripgrep \{digits\}( bench-[^"]*)?"\)', f'format!("ripgrep {{digits}} bench-{n}")', s); app.write_text(s)
elif kind == "lib":
    s = lib.read_text()
    import re; s = re.sub(r'\[Omitted long context line( bench-[^\]]*)?\]', f'[Omitted long context line bench-{n}]', s); lib.write_text(s)
elif kind == "file":
    # generic: edit.py <root> file <relpath> <n>; appends/replaces a trailing marker comment
    import re
    target = root / sys.argv[3]; n = sys.argv[4]
    t = target.read_text()
    t = re.sub(r'\n// bench-[^\n]*\n?$', '\n', t)
    if n != "reset":
        t = t.rstrip('\n') + f'\n// bench-{n}\n'
    target.write_text(t)
elif kind == "reset":
    import re
    s = app.read_text(); app.write_text(re.sub(r'format!\("ripgrep \{digits\} bench-[^"]*"\)', APP_BASE, s))
    s = lib.read_text(); lib.write_text(re.sub(r'\[Omitted long context line bench-[^\]]*\]', LIB_BASE, s))
else:
    sys.exit("unknown kind")
