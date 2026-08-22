declare module "cliui" {
  type Column = {
    text: string;
    width?: number;
    padding?: [number, number, number, number];
    align?: "left" | "right" | "center";
  };
  type UI = {
    div(...columns: Array<string | Column>): void;
    toString(): string;
  };
  export default function cliui(options?: { width?: number; wrap?: boolean }): UI;
}
