import { defineConfig } from "blume";

export default defineConfig({
  title: "Bento",
  description: "Run PHP and HTTP apps on one Linux host you own: one container per app, intent in SQLite, Docker driven directly.",
  logo: {
    image: "/bento-logo.svg",
    text: "Bento",
    href: "/",
  },
  theme: {
    accent: { light: "#A8321F", dark: "#D0402A" },
    background: { light: "#FFFBF2", dark: "#1B1815" },
    radius: "sm",
    mode: "system",
    fonts: {
      display: {
        name: "JetBrains Mono",
        variants: [
          { src: "./public/fonts/jetbrains-mono-latin-wght.woff2", weight: 400 },
          { src: "./public/fonts/jetbrains-mono-latin-wght.woff2", weight: 700 },
        ],
      },
      body: {
        name: "JetBrains Mono",
        variants: [
          { src: "./public/fonts/jetbrains-mono-latin-wght.woff2", weight: 400 },
          { src: "./public/fonts/jetbrains-mono-latin-wght.woff2", weight: 700 },
        ],
      },
      mono: {
        name: "JetBrains Mono",
        variants: [
          { src: "./public/fonts/jetbrains-mono-latin-wght.woff2", weight: 400 },
          { src: "./public/fonts/jetbrains-mono-latin-wght.woff2", weight: 700 },
        ],
      },
    },
  },
  navigation: {
    sidebar: { display: "group" },
  },
});
