---
name: Bento Compartment System
description: A calm operator workspace that organizes tasks into purposeful Bento compartments.
colors:
  background: "#f5f6f1"
  foreground: "#22352f"
  card: "#ffffff"
  primary: "#294d3f"
  primary-foreground: "#ffffff"
  secondary: "#e4ede3"
  secondary-foreground: "#294d3f"
  muted: "#edf1eb"
  muted-foreground: "#53665d"
  destructive: "#aa4534"
  success: "#246e50"
  warning: "#8d5518"
  info: "#315e78"
  border: "#dce5da"
  input: "#ccd8cb"
  ring: "#3e7760"
  sidebar: "#1e3b32"
  sidebar-foreground: "#f2f7ee"
  sidebar-primary: "#dcebd7"
  sidebar-primary-foreground: "#183c2e"
  tray-frame: "#385348"
  tray-cell: "#fffdfa"
  tray-soft: "#f1f5ed"
  tray-warm: "#fff1e7"
  dark-background: "#14241e"
  dark-foreground: "#eef3e9"
  dark-card: "#20342c"
  dark-tray-frame: "#0c2018"
  dark-tray-cell: "#22392d"
typography:
  body:
    fontFamily: "Noto Sans, Noto Sans JP, Hiragino Kaku Gothic ProN, Yu Gothic, system-ui, sans-serif"
    fontSize: "0.9375rem"
    lineHeight: 1.5
  page-title:
    fontSize: "1.65rem"
    lineHeight: 1.25
  label:
    fontSize: "0.75rem"
    fontWeight: 600
rounded:
  sm: "0.375rem"
  md: "0.5rem"
  lg: "0.75rem"
  tray: "1rem"
components:
  button-primary:
    backgroundColor: "{colors.primary}"
    textColor: "{colors.primary-foreground}"
    rounded: "{rounded.md}"
    height: "2.25rem"
    padding: "0.5rem 1rem"
  input:
    backgroundColor: "{colors.card}"
    rounded: "{rounded.md}"
    height: "2.75rem"
  tray:
    backgroundColor: "{colors.tray-frame}"
    rounded: "{rounded.tray}"
    padding: "0.55rem"
  compartment:
    backgroundColor: "{colors.tray-cell}"
    rounded: "{rounded.lg}"
  form-group:
    backgroundColor: "{colors.tray-soft}"
    rounded: "{rounded.lg}"
  navigation-active:
    backgroundColor: "{colors.sidebar-primary}"
    textColor: "{colors.sidebar-primary-foreground}"
    rounded: "{rounded.md}"
---

# Design System: Bento Compartment System

## Overview

**Creative North Star: "The Working Bento"**

Bento is a functional way of arranging work, not a food illustration. A visible tray frame divides the workspace into distinct compartments. Cell size follows the importance of the content: stack condition and exceptions lead; applications each own a box; data bindings reveal the actual service, user, databases, and path without collapsing into a text line. In forms, compartments group fields by the decision being made. The visual is calm and friendly, but its geometry does the organizing.

**Key Characteristics:**
- The dark tray seam groups neighboring cells without adding decorative shadows.
- One resource per application or binding cell, with its state and actions inside the same cell.
- Form groups contain one related decision: identity, runtime, routing, data, or resources.
- Color and icon reinforce a textual state; neither substitutes for it.

## Colors

Deep pine owns navigation, actions, and the tray frame. Warm rice-white cells give the operator room to read. Pale sage groups supporting fields; warm persimmon tint is reserved for resources that need attention. Dark mode has its own equivalents in `src/app.css`.

### Primary
- **Pine:** `primary` is for actions; `sidebar` is the route rail; `tray-frame` is the structural seam around a group of cells.

### Secondary
- **Soft sage:** `secondary` and `tray-soft` distinguish selected states and related form fields.

### Neutral
- **Rice workspace:** `background` carries the page; `tray-cell` carries compartments; `border` separates rows within them.
- **Ink:** `foreground` carries data; `muted-foreground` supports it without hiding it.
- **State tones:** `success`, `warning`, `info`, and `destructive` always accompany words and icons.

**The Compartment Rule.** A colored rectangle earns its place by containing a complete task or resource, not by displaying a lone metric.

## Typography

Noto Sans with Japanese system fallbacks keeps labels, data, and controls in one approachable register. Body size is 0.9375rem, page titles 1.65rem, and compact labels 0.75rem. Counts and codes use tabular numerals or code type only where data benefits from it. Headings identify tasks, never ornamental sections.

## Layout

The desktop shell retains a 15.5rem route rail and a flexible workspace; the rail can collapse to 4.5rem. Overview is a two-column tray with named cells, stacking at 1100px. Application cells arrange in three columns, two below 1200px, and one below 768px. Binding and service cells arrange in two columns, then one. A mobile drawer replaces the rail. Form controls share a 2.75rem height; related fields align within each form group, while different groups are separated by a clear gap.

## Elevation & Depth

Flat by default. Tray frames and tonal fields establish hierarchy; inner cells do not each acquire a border and shadow. Standard dialog elevation remains for actual overlays.

## Shapes

A tray uses a 1rem outer radius with 0.55rem seams. Its cells use a slightly tighter 0.65–0.75rem radius. Controls use 0.5–0.55rem corners. This nested corner relationship is structural rather than ornamental.

## Components

### Tray and cell
A tray is a dark frame with 0.55rem padding and gap. Cells are rice-white, independently linked or interactive where appropriate. Attention cells use the warm tint; do not nest another card inside a cell.

### Application cell
The app's mark and status lead, name and domain follow, then runtime and access facts, with lifecycle controls in the same cell. Pending operation state attaches to that app and disables conflicting actions.

### Binding cell
Engine and service form the heading. Structured rows show username or file path; database names are separate chips. The add-database action lives at the bottom of the same binding cell.

### Form group
A short heading states the decision and groups related controls. Inputs and selects fill their grid tracks and share the same height. Advanced resource limits remain available without mixing into routing or data choices.

### Navigation and status
The sidebar keeps a recognizable active route and a visible Docker signal. State badges use readable text and a consistent icon as well as color. On mobile, navigation becomes a dismissible drawer.

## Do's and Don'ts

### Do:
- **Do** let the compartment arrangement reveal what belongs together and what deserves attention.
- **Do** keep an action and its pending/error state beside the resource it affects.
- **Do** keep destructive confirmations, retained-data descriptions, and add-only binding behavior exact.

### Don't:
- **Don't** substitute a flat applications table or CSV-like database line for resource compartments.
- **Don't** use a thick frame on every individual control, or decorative bento food imagery.
- **Don't** mix unrelated fields or let select widths depend on their option text.
