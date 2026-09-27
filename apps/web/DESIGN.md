---
name: Bento Box
description: Washi paper, sumi ink, one shu accent. Work arranged in compartments of a single lacquer box.
colors:
  background: "#f4f0e6" # washi
  foreground: "#1f1c18" # sumi
  card: "#fffcf5"
  secondary: "#ece6d8"
  muted-foreground: "#756c5f"
  primary: "#c23b22" # shu (vermilion) — the only accent
  success: "#4f7a34" # matcha
  warning: "#a8741a" # kin
  info: "#2f5b87" # ai
  destructive: "#b3261e"
  seam: "#e2dac8"
  dark-background: "#12110f" # urushi
  dark-card: "#1b1916"
  dark-primary: "#e2603f"
rounded:
  control: "0.5rem"
  box: "1.25rem"
  pill: "999px"
---

# Design System: Bento Box

## Principles

- **One box, many compartments.** A `.box` is a rounded container whose `.cell`s are divided by 1px seams
  (grid gap over the seam color). Related things share a box; unrelated things get a new box.
- **Less text.** Short titles, no page descriptions unless they carry a safety fact. Labels are one or two words.
  Exact confirmations and retained-data facts are never shortened away.
- **One accent.** Shu vermilion marks the primary action, the active nav icon, and attention cells. Status colors
  (matcha, kin, ai) appear only in pills and dots, always with a word.
- **Flat and calm.** One soft shadow per box; cells are flat. No gradients or food imagery.

## Layout

- Top bar: logo left, pill navigation centered, tools right (search ⌘K, Docker dot, theme cycle, sign out).
  Below 1100px nav labels hide to icons; below 900px the navigation moves to a sticky bottom tab bar.
- Content max width 96rem. Page = `PageHeader` then boxes separated by 1.25rem.
- Box grids: `box--2`, `box--3`, `box--4`, `box--main` (1.6 : 1). Tiles use `.box > .tiles` (1–4 columns by container width). A tile shows
  facts (`.facts`), not actions; actions live on the detail page. A trailing `.tile--add` placeholder fills the
  rest of the last row.
  Everything collapses to one column at 680px.

## Components (src/app.css)

- `cell`, `cell--alert` (shu tint), `cell--muted` (footers, add forms), `cell--wide`, `cell--span2`, `col`.
- `cell__title`: small uppercase caption plus an optional right-side link or badge.
- `metric`: large tabular number with a small caption.
- `rows` / `row`: list rows; `rows--lined` adds dividers.
- `pill` (via `StateBadge`), `tag`, `chip`, `mono` (monogram), `dot`.
- `seg`: segmented control for tabs and filters (`aria-current`, `aria-pressed`, or `aria-selected`).
- `choice`: radio cards; `field`, `grid-2`, `grid-3`, `check`.
- `console`: logs and terminal surface. `timeline`: operation events.

## Do / Don't

- Do keep an action and its pending or error state inside the same cell as its resource.
- Do use `KeyValues` for facts and `metric` for single numbers.
- Don't nest boxes, add borders to individual cells, or introduce another accent color.
