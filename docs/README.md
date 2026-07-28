# Bento documentation

This directory contains Bento’s documentation site, built with [Astro](https://astro.build/) and [Starlight](https://starlight.astro.build/).

## Development

Requires Node.js 22.12 or newer.

```bash
cd docs
npm install
npm run dev
```

The local site is available at `http://localhost:4321` by default.

## Commands

| Command | Action |
| --- | --- |
| `npm run dev` | Start the development server |
| `npm run build` | Build the static site into `dist/` |
| `npm run preview` | Preview the production build |
| `npm run astro -- …` | Run the Astro CLI |

## Adding documentation

Add Markdown or MDX files beneath [`src/content/docs/`](src/content/docs/). Starlight derives routes and navigation from that directory. Site-wide configuration lives in [`astro.config.mjs`](astro.config.mjs).
