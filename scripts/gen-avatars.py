#!/usr/bin/env python3
"""Generate the avatar sprite sheets.

The avatar was an emoji in a coloured circle. An emoji is drawn by the operating
system, so it looks like a different character on every machine, cannot be
animated, and has exactly the expressions Unicode happens to have named. These
are drawn here instead: small enough to read at a glance, and ours.

Sprites are written as text, one character per pixel, because that is the only
form a person can edit later. A PNG of a 20x24 sprite is editable by nothing but
a pixel editor; this file is editable by anyone who can count squares.

The body is drawn once per character. An emotion is a handful of pixels stamped
onto the face, and a frame is a small transform of the whole — a bob, a blink, a
tail flick. Written out in full it would be three characters times five emotions
times four frames, which is sixty grids nobody would keep in step by hand.

    python3 scripts/gen-avatars.py

Writes one sheet per character to backend/avatars/: a row per emotion, a column
per frame, so the page plays it with a CSS steps() animation and no JavaScript.
"""

from __future__ import annotations

import os
from PIL import Image

W, H = 20, 24          # one sprite

# Two strips side by side: the first four frames stand and breathe, the next
# four walk. A state picks the strip — working walks, everything else stands —
# so the page switches gait by moving the window, with no second sheet to load
# and nothing to keep in sync between them.
IDLE_FRAMES = 4
WALK_FRAMES = 4
FRAMES = IDLE_FRAMES + WALK_FRAMES
EMOTIONS = [
    "neutral", "happy", "sad", "thinking", "excited",
    "sleepy", "confused", "love", "angry", "surprised",
]

# Where the face features are stamped. Fixed across characters so one emotion
# table serves all three: they are different creatures with the same head box.
EYE_Y = 7
EYE_LX, EYE_RX = 6, 12   # left edge of each eye, which is 2 px wide
MOUTH_Y = 10
MOUTH_X = 8              # 4 px wide

PALETTES = {
    "cat": {
        ".": (0, 0, 0, 0),
        "o": (38, 30, 42, 255),      # outline
        "f": (247, 172, 92, 255),    # fur
        "d": (211, 130, 56, 255),    # fur, shaded
        "w": (255, 243, 230, 255),   # belly
        "p": (243, 143, 162, 255),   # inner ear, nose
        "e": (36, 30, 48, 255),      # eye
        "h": (255, 255, 255, 255),   # highlight
        "m": (120, 70, 40, 255),     # mouth line
    },
    "bunny": {
        ".": (0, 0, 0, 0),
        "o": (44, 36, 44, 255),
        "f": (232, 226, 236, 255),
        "d": (196, 188, 204, 255),
        "w": (255, 252, 255, 255),
        "p": (246, 158, 176, 255),
        "e": (48, 38, 56, 255),
        "h": (255, 255, 255, 255),
        "m": (150, 110, 120, 255),
    },
    "robot": {
        ".": (0, 0, 0, 0),
        "o": (26, 32, 42, 255),
        "f": (158, 206, 230, 255),
        "d": (106, 158, 192, 255),
        "w": (226, 244, 252, 255),
        "p": (255, 194, 96, 255),    # antenna light
        "e": (58, 182, 232, 255),    # eye glow
        "h": (255, 255, 255, 255),
        "m": (58, 182, 232, 255),
    },
}

# ---------------------------------------------------------------------------
# Bodies. The face area is left blank fur; the emotion table draws into it.
# ---------------------------------------------------------------------------

CAT_BODY = [
    "...oo..........oo...",
    "..offo........offo..",
    "..ofpfo......ofpfo..",
    "..offfoooooooffffo..",
    "..offffffffffffffo..",
    "..offffffffffffffo..",
    ".offffffffffffffffo.",
    ".offffffffffffffffo.",
    ".offffffffffffffffo.",
    ".offffffffffffffffo.",
    ".offffffffffffffffo.",
    "..offffffffffffffo..",
    "...ooofffffffooo....",
    ".....offffffffo.....",
    "....offffffffffo....",
    "....offwwwwwwffo....",
    "....offwwwwwwffo....",
    "....offwwwwwwffo....",
    "....offwwwwwwffo....",
    "....offffffffffo....",
    ".....offooooffo.....",
    ".....offo..offo.....",
    "......ooo...ooo.....",
    "....................",
]

BUNNY_BODY = [
    ".....oo....oo.......",
    "....ofpfo.ofpfo.....",
    "....ofpfo.ofpfo.....",
    "....offfo.offfo.....",
    "....offfoooffffo....",
    "...offffffffffffo...",
    "..offffffffffffffo..",
    ".offffffffffffffffo.",
    ".offffffffffffffffo.",
    ".offffffffffffffffo.",
    ".offffffffffffffffo.",
    "..offffffffffffffo..",
    "...ooofffffffooo....",
    ".....offffffffo.....",
    "....offffffffffo....",
    "....offwwwwwwffo....",
    "....offwwwwwwffo....",
    "....offwwwwwwffo....",
    "....offwwwwwwffo....",
    "....offffffffffo....",
    ".....offooooffo.....",
    ".....offo..offo.....",
    "......ooo...ooo.....",
    "....................",
]

ROBOT_BODY = [
    ".........pp.........",
    ".........oo.........",
    "....oooooooooooo....",
    "...offffffffffffo...",
    "...offffffffffffo...",
    "...offwwwwwwwwffo...",
    "...offwwwwwwwwffo...",
    "...offwwwwwwwwffo...",
    "...offwwwwwwwwffo...",
    "...offwwwwwwwwffo...",
    "...offwwwwwwwwffo...",
    "...offffffffffffo...",
    "....oooffffffooo....",
    ".....offffffffo.....",
    "....offffffffffo....",
    "....offdddddddfo....",
    "....offdwwwwddfo....",
    "....offdwwwwddfo....",
    "....offdddddddfo....",
    "....offffffffffo....",
    ".....offoooofffo....",
    ".....offo..offo.....",
    "......ooo...ooo.....",
    "....................",
]

BODIES = {"cat": CAT_BODY, "bunny": BUNNY_BODY, "robot": ROBOT_BODY}

# ---------------------------------------------------------------------------
# Faces. Each entry draws relative to the eye and mouth anchors above, so the
# same table works on all three heads.
#
# An eye is 2x2. Two pixels wide is the floor: at one pixel it is a speck that
# reads as dirt on the screen rather than as a face looking at you, which is
# what the first draft of these got wrong.
# ---------------------------------------------------------------------------

def eyes_open(mark: str = "e"):
    px = []
    for x0 in (EYE_LX, EYE_RX):
        for dy in (0, 1):
            for dx in (0, 1):
                px.append((x0 + dx, EYE_Y + dy, mark))
    # A highlight in the top-left of each eye is what makes it wet rather than
    # painted, and it is one pixel.
    px.append((EYE_LX, EYE_Y, "h"))
    px.append((EYE_RX, EYE_Y, "h"))
    return px


def eyes_closed():
    return [(x0 + dx, EYE_Y + 1, "o") for x0 in (EYE_LX, EYE_RX) for dx in (0, 1)]


def eyes_happy():
    # Curved shut, the ^^ of a smile. Drawn as two pixels raised at the outside.
    return [
        (EYE_LX, EYE_Y + 1, "o"), (EYE_LX + 1, EYE_Y, "o"),
        (EYE_RX, EYE_Y, "o"), (EYE_RX + 1, EYE_Y + 1, "o"),
    ]


def eyes_sad():
    return eyes_open() + [
        (EYE_LX, EYE_Y - 1, "o"), (EYE_LX + 1, EYE_Y - 1, "o"),
        (EYE_RX, EYE_Y - 1, "o"), (EYE_RX + 1, EYE_Y - 1, "o"),
    ]


def eyes_sleepy():
    # Shut and curved the other way from happy: the lids come down at the
    # outside rather than up, which is the difference between content and
    # about to fall over.
    return [
        (EYE_LX, EYE_Y, "o"), (EYE_LX + 1, EYE_Y + 1, "o"),
        (EYE_RX, EYE_Y + 1, "o"), (EYE_RX + 1, EYE_Y, "o"),
    ]


def eyes_confused():
    # One wide, one narrowed. Asymmetry is the whole expression; two identical
    # eyes with a wavy mouth reads as seasick.
    px = []
    for dy in (0, 1):
        for dx in (0, 1):
            px.append((EYE_LX + dx, EYE_Y + dy, "e"))
    px.append((EYE_LX, EYE_Y, "h"))
    px += [(EYE_RX, EYE_Y + 1, "o"), (EYE_RX + 1, EYE_Y + 1, "o")]
    return px


def eyes_angry():
    # Brows slanting in. Drawn above the eye rather than over it, so the eye
    # stays open and the character looks cross rather than asleep.
    return eyes_open() + [
        (EYE_LX, EYE_Y - 1, "o"), (EYE_LX + 1, EYE_Y - 2, "o"),
        (EYE_RX + 1, EYE_Y - 1, "o"), (EYE_RX, EYE_Y - 2, "o"),
    ]


def blush():
    """Two pink patches at the cheeks, for love."""
    return [(EYE_LX - 2, EYE_Y + 2, "p"), (EYE_LX - 1, EYE_Y + 2, "p"),
            (EYE_RX + 2, EYE_Y + 2, "p"), (EYE_RX + 3, EYE_Y + 2, "p")]


def eyes_wide():
    px = eyes_open()
    for x0 in (EYE_LX, EYE_RX):
        for dx in (0, 1):
            px.append((x0 + dx, EYE_Y + 2, "e"))
    return px


def mouth(shape: str):
    y = MOUTH_Y
    x = MOUTH_X
    if shape == "flat":
        return [(x + 1, y, "m"), (x + 2, y, "m")]
    if shape == "smile":
        return [(x, y, "m"), (x + 1, y + 1, "m"), (x + 2, y + 1, "m"), (x + 3, y, "m")]
    if shape == "frown":
        return [(x, y + 1, "m"), (x + 1, y, "m"), (x + 2, y, "m"), (x + 3, y + 1, "m")]
    if shape == "open":
        return [(x + 1, y, "o"), (x + 2, y, "o"),
                (x + 1, y + 1, "o"), (x + 2, y + 1, "o")]
    if shape == "small":
        return [(x + 1, y, "m")]
    if shape == "o":
        # A round surprised mouth: two pixels, one above the other.
        return [(x + 1, y, "o"), (x + 1, y + 1, "o"),
                (x + 2, y, "o"), (x + 2, y + 1, "o")]
    if shape == "wave":
        return [(x, y, "m"), (x + 1, y + 1, "m"), (x + 2, y, "m"), (x + 3, y + 1, "m")]
    raise ValueError(shape)


FACES = {
    "neutral":   eyes_open() + mouth("flat"),
    "happy":     eyes_happy() + mouth("smile"),
    "sad":       eyes_sad() + mouth("frown"),
    "thinking":  eyes_open() + mouth("small"),
    "excited":   eyes_wide() + mouth("open"),
    "sleepy":    eyes_sleepy() + mouth("small"),
    "confused":  eyes_confused() + mouth("wave"),
    "love":      eyes_happy() + mouth("smile") + blush(),
    "angry":     eyes_angry() + mouth("frown"),
    "surprised": eyes_wide() + mouth("o"),
}

# The blinking frame of each emotion. Happy eyes are already shut and sad ones
# have to stay sad, so neither blinks — a character that blinks out of a wince
# reads as confused rather than alive.
BLINKS = {
    "neutral": eyes_closed() + mouth("flat"),
    "thinking": eyes_closed() + mouth("small"),
    "excited": eyes_closed() + mouth("open"),
    "confused": eyes_closed() + mouth("wave"),
    "surprised": eyes_closed() + mouth("o"),
}

# What each emotion does with its ears, drawn only on characters that have them.
# thinking tilts one ear back; sad drops both.
EAR_ROWS = 4


def to_grid(rows: list[str]) -> list[list[str]]:
    return [list(r) for r in rows]


def stamp(grid: list[list[str]], pixels) -> None:
    for x, y, ch in pixels:
        if 0 <= x < W and 0 <= y < H:
            grid[y][x] = ch


def bob(grid: list[list[str]], dy: int) -> list[list[str]]:
    """Shift the whole sprite down by dy, which is the breathing."""
    if dy == 0:
        return grid
    blank = ["."] * W
    return [list(blank) for _ in range(dy)] + grid[:-dy]


# Legs start under the belly, not at the very bottom: at three rows plus a foot
# they are long enough for a step to be visible, which two rows was not — the
# first walk cycle moved a two-pixel stub and read as a shiver.
LEG_TOP = 19


def clear_legs(grid: list[list[str]]) -> None:
    for y in range(LEG_TOP, H):
        for x in range(W):
            grid[y][x] = "."


def draw_leg(grid: list[list[str]], x: int, height: int) -> None:
    """One leg: three pixels wide, `height` tall, with a foot under it."""
    for i in range(height):
        y = LEG_TOP + i
        if y >= H:
            return
        grid[y][x], grid[y][x + 1], grid[y][x + 2] = "o", "f", "o"
    y = LEG_TOP + height
    if y < H:
        grid[y][x], grid[y][x + 1], grid[y][x + 2] = "o", "o", "o"


# A walk in four frames: stride, pass, opposite stride, pass. The two passing
# frames are the same pose, which is what a walk cycle actually is — the legs
# cross the same place going each way, and inventing a difference to avoid the
# repeat is what makes a small sprite look like it is limping.
# The stride stays open in both contact frames and only the heights swap: which
# foot is planted is what alternates. Moving the legs toward each other instead
# drew them as one block in the middle, which reads as standing still with a
# wide waist.
WALK_LEGS = [
    ((3, 3), (13, 2)),   # left planted, right lifted
    ((5, 3), (12, 3)),   # passing
    ((3, 2), (13, 3)),   # right planted, left lifted
    ((5, 3), (12, 3)),   # passing
]

# Standing: both legs down, evenly placed.
IDLE_LEGS = ((5, 3), (12, 3))


def legs(grid: list[list[str]], pose) -> None:
    clear_legs(grid)
    (lx, lh), (rx, rh) = pose
    draw_leg(grid, lx, lh)
    draw_leg(grid, rx, rh)


def tail(grid: list[list[str]], phase: int, char: str) -> None:
    """A tail that sways, for the two that have one."""
    if char == "robot":
        return
    # Three positions, so the sway is not a metronome.
    shapes = {
        0: [(16, 17), (17, 16), (17, 15)],
        1: [(16, 17), (17, 17), (18, 16)],
        2: [(16, 17), (17, 16), (18, 15)],
        3: [(16, 17), (17, 17), (18, 17)],
    }
    for x, y in shapes[phase]:
        if 0 <= x < W and 0 <= y < H:
            grid[y][x] = "d"


def frame(char: str, emotion: str, i: int) -> list[list[str]]:
    """Frame i of the strip: 0-3 stand, 4-7 walk."""
    walking = i >= IDLE_FRAMES
    phase = i - IDLE_FRAMES if walking else i

    grid = to_grid(BODIES[char])
    # A walking character does not blink: the eye has a quarter of a second per
    # frame and the legs are already using it.
    face = BLINKS[emotion] if (not walking and phase == 2 and emotion in BLINKS) else FACES[emotion]
    stamp(grid, face)
    tail(grid, phase, char)

    legs(grid, WALK_LEGS[phase] if walking else IDLE_LEGS)
    if walking:
        # The body rises on the passing frames, which is where the weight is
        # over one leg. Standing bobs on the off-beats instead — that one is a
        # breath, this one is a step.
        return bob(grid, 0 if phase % 2 else 1)
    return bob(grid, 1 if phase % 2 else 0)


def check(char: str) -> None:
    palette = PALETTES[char]
    body = BODIES[char]
    assert len(body) == H, f"{char}: {len(body)} rows, want {H}"
    for y, row in enumerate(body):
        assert len(row) == W, f"{char}: row {y} is {len(row)} wide, want {W}"
        for ch in row:
            assert ch in palette, f"{char}: unknown pixel {ch!r} in row {y}"


def build(char: str) -> Image.Image:
    palette = PALETTES[char]
    sheet = Image.new("RGBA", (W * FRAMES, H * len(EMOTIONS)), (0, 0, 0, 0))
    for row, emotion in enumerate(EMOTIONS):
        for i in range(FRAMES):
            grid = frame(char, emotion, i)
            for y in range(H):
                for x in range(W):
                    sheet.putpixel((i * W + x, row * H + y), palette[grid[y][x]])
    return sheet


def main() -> None:
    here = os.path.dirname(os.path.abspath(__file__))
    out = os.path.join(os.path.dirname(here), "backend", "avatars")
    os.makedirs(out, exist_ok=True)
    for char in BODIES:
        check(char)
        sheet = build(char)
        path = os.path.join(out, f"{char}.png")
        sheet.save(path)
        print(f"{path}  {sheet.width}x{sheet.height}  "
              f"{len(EMOTIONS)} emotions x {FRAMES} frames of {W}x{H}")


if __name__ == "__main__":
    main()
