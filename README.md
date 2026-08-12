**English** · [Español](README.es.md)

# dictador

Global push-to-talk dictation for Linux/X11, in Go. Hold a key, talk, let go, and
the text lands wherever your cursor was.

This is the Go rewrite of [dictado](https://github.com/neitanod/dictado), which
is in Python and works. What changes here: a single binary with no venv, no GUI
toolkit, and no `xdotool`, `xclip` or `xprop` — X11 is spoken directly — plus a
daemon that is a state machine over channels instead of Qt signals and three
timers.

![The overlay while you dictate](docs/overlay-parcial.png)

## Install

You need Go 1.21 or newer and `parec` (the `pulseaudio-utils` package), which
ships with any Ubuntu running PipeWire.

```bash
git clone https://github.com/neitanod/dictador
cd dictador
make install          # builds and drops the binary in ~/.local/bin
dictador doctor       # checks everything is where it should be
```

`make install` touches nothing system-wide: it builds and copies. To have it
start on every login, `dictador service install`.

## Use

```bash
dictador run          # starts the daemon and waits for the key
```

With the daemon running, hold **AltGr + Right Control**, talk, and let go. The
text is pasted where the cursor was.

### Picking another key

```bash
dictador keys control                       # see what your keyboard has
dictador config set hotkey.key "Menu"       # and pick yours
```

A single name works (`"Pause"`), and so does a combination
(`"AltGr+Control_R"`): the last one is the trigger, everything before it has to
be held down. The order you press them in doesn't matter. There are aliases for
the keys nobody wants to look up in `xmodmap`: `AltGr`, `Ctrl`, `Alt`, `Shift`,
`Super`, `RightCtrl`, `LeftCtrl`, `Menu`.

### Keeping keys as keys

The dictation key is never grabbed: it's watched through raw XInput2, so it
keeps working for the rest of the system. And so that a real Right Control
doesn't fire a dictation, there's a threshold: recording starts only after
holding it for 180 ms. Press any other key meanwhile and it cancels.

```bash
dictador config set hotkey.hold_threshold_ms 250
dictador config set hotkey.cancel_on_other_key false
```

### What happens when you let go

| `action.on_release` | what it does |
|---|---|
| `paste` | copies and pastes into the window you were in (default) |
| `type` | types the text out, character by character |
| `clipboard` | leaves it on the clipboard and nothing else |
| `keep_open` | copies it and keeps the window up so you can read it |

In terminals it pastes with `Ctrl+Shift+V`, which is what works there; they're
recognized by their `WM_CLASS`.

### Toggle mode

If you'd rather press once to start and again to stop:

```bash
dictador config set hotkey.mode toggle
```

## Picking an engine

There are three, and switching costs no restart. The comfortable way is to
**click the little window** while you're dictating: that cuts the dictation short
and opens the configuration.

![The configuration](docs/config-web.png)

From the terminal too:

```bash
dictador config web                      # the same page, without the daemon
dictador config set stt.engine chrome
```

| engine | where it transcribes | cost | live text |
|---|---|---|---|
| `faster-whisper` | on your machine | free | yes |
| `chrome` | Google's servers | free | yes |
| `google` | Google Cloud | pay per use | no |

### Local Whisper

The default. Transcribes on your machine, no API key, your voice goes nowhere.
It needs a `whisper-server` — the one that ships with
[whisper.cpp](https://github.com/ggerganov/whisper.cpp) — listening alongside:

```bash
whisper-server -m models/ggml-small.bin --host 127.0.0.1 --port 8080
```

The model stays loaded there, so each dictation costs one local HTTP call and
live text stays cheap. If the server isn't up, `dictador doctor` hands you this
exact line.

### Chrome, or how to dictate to Google for free

Google has two speech services sharing a surname. **Cloud Speech-to-Text** is the
commercial product: API key, billed per minute. The **Web Speech API** is the one
Chrome hands to web pages for free — the little microphone on google.com,
Android's dictation — and it can't be called from outside the browser, because
the keys are compiled into Chrome.

The `chrome` engine uses it from the inside: it launches a headless Chrome on a
local page served by the app itself, and that page recognizes and returns the
text.

Measured on a laptop, natural voice, same sentence:

| engine | accuracy | latency on release |
|---|---|---|
| `chrome` | 100% | **0.12 s** |
| `faster-whisper small` int8 | 90% | 2.22 s |

Free, more accurate and about twenty times faster than local Whisper, and it
throws in live partials because Chrome emits them itself.

What you're paying: **your voice travels to Google**, it needs internet, and a
resident Chrome eats ~200 MB of RAM. It's an internal Chrome endpoint, so if
Google changes it, it breaks. That's why the default is still Whisper and
choosing this engine is your call, explicitly.

```bash
dictador config set stt.engine chrome
```

### Google Cloud Speech-to-Text

For when you want Google's quality without a resident Chrome and don't mind
paying for it. Needs an API key from
[console.cloud.google.com](https://console.cloud.google.com) → APIs →
Speech-to-Text → Credentials:

```bash
dictador config set stt.engine google
dictador config set stt.google_api_key "AIza…"
```

Or through the environment, to keep it out of a file: `GOOGLE_API_KEY`.

This engine draws no live text on purpose: every partial would be a network trip
and a billed call.

## Commands

| command | what it does |
|---|---|
| `dictador run` | the daemon with the global key |
| `dictador once` | records once and writes the text to stdout |
| `dictador bench` | compares the engines using your voice |
| `dictador doctor` | checks everything is where it should be |
| `dictador keys [filter]` | lists the keys in the current map |
| `dictador config [show\|init\|edit\|path\|set\|web]` | view or edit the configuration |
| `dictador history [-n N]` | the last dictations |
| `dictador service [install\|uninstall\|status]` | autostart on login |

They all take `--json`, so the CLI can be scripted:

```bash
dictador once -s 5 --json | jq -r .text
dictador doctor --json | jq '.checks[] | select(.ok == false)'
```

Exit codes: `0` fine · `1` error · `2` usage error · `130` cancelled with Ctrl+C.

## Configuration

It lives in `~/.config/dictador/config.toml`. `dictador config init` creates it
with every value commented, and `dictador config edit` opens it in your
`$EDITOR`.

If it doesn't exist yet but the Python `dictado` one does
(`~/.config/dictado/config.toml`), that one is read: the port starts with the
configuration the machine already had.

**Saving doesn't clobber the comments.** `dictador config set` edits the line
that changes and leaves the rest of the file alone, comments included.

```toml
[hotkey]
key = "AltGr+Control_R"
mode = "hold"              # hold = push-to-talk | toggle = press/press
hold_threshold_ms = 180
cancel_on_other_key = true

[audio]
device = ""                # empty = PipeWire's default source
sample_rate = 16000

[stt]
engine = "faster-whisper"  # faster-whisper | chrome | google
whisper_server_url = "http://127.0.0.1:8080"
language = "es"            # "" to autodetect
partial_interval_ms = 900
initial_prompt = ""        # jargon or proper nouns you want it to get right

[action]
on_release = "paste"       # paste | type | clipboard | keep_open
restore_focus = true
trailing_space = false
strip_final_period = false

[overlay]
enabled = true
hide_delay_ms = 1400

[limits]
max_seconds = 120
min_seconds = 0.35
```

## How it's built

Most of it is unsurprising. These four aren't, which is why they're written down.

**The key is watched without grabbing it.** A desktop global shortcut tells you
about the press and never about the release, and push-to-talk needs both. The
answer is raw XInput2 on the root window: we hear everything and the key keeps
serving the rest of the system. Two details that cost time: `xgb` — Go's X11
library — **doesn't ship the XInput extension**, so the two requests we need are
assembled byte by byte; and its read loop reads events in fixed 32-byte chunks
without consuming the extra payload a GenericEvent may carry behind it, so the
socket is wrapped in a filter that reframes them. Without that, the day a
keyboard reports valuators the whole X connection turns to garbage.

**Modifiers are asked of X.** Accumulating presses and releases looks simpler
until another app grabs the keyboard and a release goes missing: that modifier
stays marked as held forever. `QueryKeymap` says which keys are actually down,
right now.

**The clipboard belongs to the daemon.** In X, whoever copied is the one who
serves the content when someone pastes. The Python version left an `xclip`
process alive per dictation; here the app owns the selection itself. The trap:
claiming the selection with a timestamp later than the server's clock **is
ignored silently** — no error, nothing, the clipboard just comes up empty — so
the time is asked of the server before the selection is.

**Pasting goes to the real focus.** Sending the event to a specific window means
`XSendEvent`, and half a dozen toolkits discard synthetic events. The window is
activated and the keys are typed at the focus, with any held modifiers released
first: if you let go of the dictation key with AltGr still down, a synthetic
Ctrl+V would come out as Ctrl+AltGr+V and paste nothing.

**The overlay is painted by hand.** It's a 32-bit ARGB override-redirect window:
the window manager doesn't touch it, doesn't decorate it and doesn't give it the
keyboard, which is what it takes for it not to steal the cursor from the field
you're dictating into. The frame is rasterized with `x/image` — rounded
rectangle, antialiased text in whatever font `fc-match` reports — and shipped
with `PutImage` in bands of rows, because a whole 780-pixel-wide image doesn't
fit in a single X request.

## Checking that it works

```bash
bash tests/run-all.sh
```

Runs `gofmt`, `go vet`, the unit tests, the race detector, a check that the
overlay actually paints (it opens the window in each state and measures that the
screen stops being black, leaving the screenshots behind to look at), and an
end-to-end pass on a virtual display (`Xvfb`) that walks the whole path: holds the key, records,
transcribes against a fake engine, and verifies the text reaches the clipboard
**and** that the synthetic Ctrl+V drops it into a window waiting for a paste.
It's the only way to know the hotkey, the focus and the paste still work
together.

## Limitations

**X11 only.** On Wayland no app can watch the global keyboard or type into
another window, and that's by design. The way out is the `GlobalShortcuts`
portal with `layer-shell` and `libei`, and it isn't there yet.

**Live text depends on the engine.** With `chrome` and with `faster-whisper` it's
drawn while you talk; with `google` it isn't, because every partial is billed.

**The little window needs a compositor.** It's painted on a 32-bit ARGB window,
so a session without compositing would get no transparency. When the screen
offers no 32-bit visual — or when there's no TrueType font around — the daemon
says so and falls back to a desktop notification that updates in place.

**Local Whisper needs a separate server.** The binary doesn't carry the model:
it talks to a `whisper-server` over local HTTP. Putting `libwhisper` inside the
binary is possible and brings CGO along with it, which is what this port has been
dodging.
