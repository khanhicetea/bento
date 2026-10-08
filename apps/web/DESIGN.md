---
name: Bento Box
description: A black lacquer box of rice-paper compartments. Fill colour is meaning, text reads like a checklist, one shu accent acts.
colors:
  washi: "#F4EFE4" # page ground
  urushi: "#241E1A" # box rim and seams; also sumi text
  gohan: "#FFFBF2" # default compartment
  line: "#DDD3C1" # hairlines, field borders
  sumi: "#241E1A" # primary text
  ink-2: "#5E554B" # secondary text, meta
  shu: "#D0402A" # the only accent: primary action, active state
  shu-deep: "#A8321F" # pressed / hover, accent text on tints
  ume: "#FBE3DC" # alert compartment
  tamago: "#FBF0CF" # caution / in-progress compartment
  nori: "#1B1815" # console compartment
  ok: "#3F7A3A" # matcha
  busy: "#2F5B87" # ai
  caution: "#9A6A12" # kin
  fail: "#B3261E"
  idle: "#6B6258"
rounded:
  rim: "26px"
  cell: "20px"
  control: "12px"
  pill: "999px"
---

# Design System: Bento Box

The reference canvas (brand, foundations, shape rules, components and every main screen) is the Design artifact
"Bento UI Redesign": https://claude.ai/artifact/Tphv5P2X8ktDrfnF5QgrNg. This file is the source of truth for code;
when the two disagree, update both.

## 1. Principles

1. **One topic, one box.** A box is a dark lacquer rim holding compartments (cells). Related facts share a box.
2. **Fill colour is meaning.** A cell's fill says what kind of thing it is, never decoration (see §4).
3. **Checklist voice.** Short caps labels, verb-first buttons, numbers with units. No paragraphs (see §6).
4. **One accent.** Shu marks the primary action and the active nav item. Nothing else is red-orange.
5. **State never by colour alone.** Every status is icon or glyph + word.
6. **Safety text is never shortened.** Exact confirmations, retained-data facts and Caution/Warning blocks stay
   complete.

## 2. Brand

- **Mark:** `public/bento-logo.svg`, a shu box with a rice compartment (face: two eyes and a smile), a matcha cell
  and a tamago cell. Works down to 16 px. One-colour version: cream on shu.
- **Wordmark:** `bento` in Barlow 700, letter-spacing −0.04em, followed by a shu `.`.
- **Mascot "Ben":** `public/bento-mascot-{ok,busy,alert,idle}.svg`, the mark as a character with feet and
  chopstick antennae. Moods map to state and are the only place he appears:

  | Mood    | Meaning                     | Used in                                     |
  | ------- | --------------------------- | ------------------------------------------- |
  | `ok`    | Healthy, done               | Status cells when everything is fine        |
  | `busy`  | Operation in progress       | Running operation cells                     |
  | `alert` | Failed, needs action        | Ume attention cells, "not set up" cells     |
  | `idle`  | Stopped, empty, off         | Empty states, disabled features             |

  Keep him at 64–128 px in cells, up to 250 px on the login screen. Never as a background pattern.

## 3. Foundations

### Colour

Tokens are in the front matter. Contrast rules: body text is `sumi` or `ink-2` on `gohan`/`washi`; status words on
tints use the darker text variants (`#2F5F2B` on ok tint, `#2F5B87` on busy tint, `#7A5410` on tamago,
`#A8321F` on ume). Tints for pills: ok `#E3EDD6`, busy `#DCE7F2`, caution `#FBF0CF`, fail `#FBE3DC`, idle
`#ECE6D8`.

Dark theme keeps the same structure: rim `#0E0C0B`, ground `#12110F`, cell `#1B1916`, accent `#E2603F`, nori stays
darkest. Fill meanings do not change between themes.

### Type

| Role    | Font                         | Size / line | Notes                         |
| ------- | ---------------------------- | ----------- | ----------------------------- |
| Display | Barlow 600                   | 40 / 44     | Metric numbers use mono       |
| Title   | Barlow 600                   | 28–32 / 34  | Page `h1`, one per page       |
| Head    | Barlow 600                   | 18 / 24     | Item names in tiles           |
| Body    | Barlow 400–500               | 15 / 22     | Max one short line            |
| Label   | Barlow Semi Condensed 600    | 12 / 16     | CAPS, letter-spacing 0.1em    |
| Mono    | JetBrains Mono 400–600       | 12–14       | IDs, numbers, paths, commits  |

### Space and shape

- Spacing scale: 4, 8, 12, 16, 24, 32, 48 px.
- Rim radius 26, cell radius 20 (= rim − seam), control radius 12, pills fully round.
- Seam and rim padding: 6 px. Touch targets ≥ 44 px. Content max width 1440 px.

### Icons

Lucide stroke icons, 2 px stroke, 18–22 px, always next to a word (icon-only buttons need `aria-label`).
Fixed meanings: Home `layout-grid`, Apps `package`, Activity `activity`, Ingress `network`, Backups `archive`,
System `server`, Deploy `rocket`, Restart `rotate-cw`, Start `play`, Stop `square`, Data `database`,
Domain `globe`, TLS `lock`, Logs/Terminal `terminal`, Schedule `clock`, Delete `trash-2`.

## 4. Bento shape rules

**Anatomy:** rim (urushi, r26, pad 6) → seams (6 px, the rim shows through) → cells (r20). A cell has a head
(icon + LABEL, at most one link or badge on the right), a body with one idea, and an optional foot with actions
above a hairline.

**Cell kinds**

| Kind     | Fill           | Means                                        |
| -------- | -------------- | -------------------------------------------- |
| Gohan    | `gohan`        | Default: facts, lists, metrics               |
| Ume      | `ume`          | Alert: failed, needs action. Max one per box, top-left |
| Tamago   | `tamago`       | Caution: pending, in progress, unsaved, near limit |
| Nori     | `nori`         | Console: logs, terminal, raw output          |
| Kara     | `washi` + 2 px dashed/inset `#B9AF9F` | Empty slot: "+ New …", fills the last row |

**Grid:** 12 columns. S = 3 (metric), M = 4 (tile), L = 6 (list), XL = 8 (main), full = 12. Row unit 120 px.
Below 1060 px S becomes 6 and the rest 12; below 640 px everything stacks to one column.

**Rules**

1. One topic, one box. Never put a box inside a box.
2. Cell radius = rim radius − seam.
3. Read order: alert → numbers → lists → actions.
4. An action lives in the cell of the resource it changes, with its pending/error state.
5. Fill every row: end with a Kara cell or a span.
6. Navigation is itself a small rim: the active item is a gohan cell inside it.

## 5. Components

| Component       | Spec                                                                                         |
| --------------- | -------------------------------------------------------------------------------------------- |
| Button          | 44 px, r12, Label type. Primary = shu fill (one per cell). Secondary = 2 px sumi inset. Ghost = text. Danger = 2 px fail inset; solid fail only inside a confirm. Busy = spinner + "-ING" verb, disabled. |
| Icon button     | 44 × 44, same kinds, `aria-label` required                                                   |
| Status pill     | 26–28 px, tint + glyph + CAPS word: ✓ RUNNING, ◌ STARTING/DEPLOYING, ▲ DEGRADED, ✕ FAILED, ■ STOPPED, ◷ QUEUED, ? UNKNOWN (outlined) |
| Dot             | 10 px, dense rows only, always with a word; pulse ring only while busy                       |
| Segments        | Square, sumi-filled active item: page sections (`role="tab"`). Round with counts: filters (`aria-pressed`) |
| Field           | CAPS label above, 44 px input, 2 px `line` border, sumi on focus; error = fail border + `✕ REASON` |
| Switch          | 52 × 30, ok when on, idle when off, label + ON/OFF word beside it                            |
| Metric          | Mono number 30–48 px + small unit, CAPS caption under; optional bar meter turns caution at ≥ 80 % |
| Fact row        | `LABEL ······ value`: dotted leader between caption and mono value                          |
| Operation       | Tamago cell: title + `n / m`, progress bar, step list ✓ done, ● current, ○ next              |
| Notice          | Caution (tamago, ▲), Warning (ume, !), Note (busy tint, i): CAPS heading + one line          |
| Toast           | Nori bar: ✓ + VERB + mono target + one link                                                  |
| Empty state     | Kara cell, mascot `idle`, CAPS title, ≤ 4-word line, one primary action                     |
| Confirm         | Lists what is Removed vs Retained as fact rows, then "Type `phrase`" field; destructive button disabled until exact match |

## 6. Voice

| Part   | Rule                                    | Example                         |
| ------ | --------------------------------------- | ------------------------------- |
| Label  | Noun, caps, ≤ 2 words                   | `MEMORY`, `LAST BACKUP`         |
| Button | Verb first                              | `DEPLOY`, `BACK UP NOW`         |
| State  | Icon + word                             | `✕ FAILED`                      |
| Value  | Number + unit, mono                     | `412 / 512 MB`                  |
| Fact   | Item ····· value                        | `TLS ····· ✓ VALID · 61 D`      |
| Risk   | Caution / Warning block, full sentence  | `Volume shop-data is kept after removal.` |

Avoid "please", "successfully", "currently", "click here", and page descriptions. Unknown values are shown as
`—`, never guessed.

## 7. Screens

- **Shell:** logo left, rim-styled nav centre, tools right (search ⌘K, Docker dot, theme, sign out). Nav labels hide
  below 1060 px; below 640 px nav becomes a bottom rim tab bar.
- **Home:** Ume attention cell (with mascot) → Apps / Operations / Data metrics → Stack services → Apps list +
  Activity.
- **Apps:** search + round filters, tiles where fill = state (ume failed, tamago deploying), trailing Kara.
- **App detail:** header with state pill and actions, square section tabs, operation cell, runtime/resources/
  domains/data facts, nori log; Settings shows Removed vs Retained before delete.
- **Activity:** operations list + detail cell whose fill and mascot follow the operation state; event timeline.
- **Ingress:** Edge / Tunnel / Utils. Edge status + traffic metrics, routes, ACME, proxy tiles + Kara.
- **Backups:** status row (last, next, schedules, not covered) then Files / App backups / Runs / Schedules.
  Restore requires `replace <db>`.
- **System:** Overview (Bento, Docker, arch; reconciliation; data services; stack; retained data with
  `delete` prune) and Images / Volumes / Networks lists with Used / Unused / Retained / Other stack tags.

## 8. Do / Don't

- Do keep an action and its pending or error state in the same cell as its resource.
- Do use fact rows for facts and metrics for single numbers.
- Don't nest boxes, add borders or left stripes to cells, use gradients, emoji or food imagery.
- Don't introduce another accent colour or a new cell kind without adding it here.
- Don't shorten exact confirmations: `delete <slug>`, `delete`, `replace <db>`, `export`, `delete <proxy>`.
