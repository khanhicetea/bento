import { describe, expect, test } from "bun:test";
import { envFromText, envToText } from "./env.ts";

describe("envToText", () => {
  test("skips rows with a blank key and joins KEY=value with newlines", () => {
    const text = envToText([
      { key: "A", value: "1" },
      { key: "  ", value: "ignored" },
      { key: "", value: "ignored" },
      { key: "B", value: "two words" },
    ]);
    expect(text).toBe("A=1\nB=two words");
  });

  test("renders no rows as an empty string", () => {
    expect(envToText([])).toBe("");
  });
});

describe("envFromText", () => {
  test("skips blank lines and # comments", () => {
    expect(envFromText("# comment\n\nA=1\n   \n  # indented comment\nB=2\r\nC=3")).toEqual([
      { key: "A", value: "1" },
      { key: "B", value: "2" },
      { key: "C", value: "3" },
    ]);
  });

  test("strips a leading export", () => {
    expect(envFromText("export A=1\nexport   B=2")).toEqual([
      { key: "A", value: "1" },
      { key: "B", value: "2" },
    ]);
  });

  test("splits on the first =", () => {
    expect(envFromText("A=b=c")).toEqual([{ key: "A", value: "b=c" }]);
  });

  test("treats a line without = as a key with an empty value", () => {
    expect(envFromText("LONELY")).toEqual([{ key: "LONELY", value: "" }]);
  });

  test("round trips plain values", () => {
    const rows = [
      { key: "APP_ENV", value: "production" },
      { key: "URL", value: "https://example.test/?a=b" },
      { key: "EMPTY", value: "" },
    ];
    expect(envFromText(envToText(rows))).toEqual(rows);
  });

  test("round trips values the parser would otherwise alter", () => {
    const values = ["plain", '"x"', "'*'", " padded ", 'a "quoted" word', '"unbalanced', "", '"'];
    for (const value of values) {
      expect(envFromText(envToText([{ key: "K", value }]))).toEqual([{ key: "K", value }]);
    }
  });
});
