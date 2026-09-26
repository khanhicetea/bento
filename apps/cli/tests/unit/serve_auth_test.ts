import { expect, test } from "bun:test";
import { validateWebAuth } from "../../src/commands/subcommands/serve.ts";

test("container web server requires valid Basic credentials before startup", () => {
  expect(() => validateWebAuth(undefined, true)).toThrow("WEB_BASIC_AUTH is required");
  expect(() => validateWebAuth("", true)).toThrow("WEB_BASIC_AUTH must be");
  expect(() => validateWebAuth("operator:", true)).toThrow("WEB_BASIC_AUTH must be");
  expect(() => validateWebAuth("operator:strong-password", true)).not.toThrow();
  expect(() => validateWebAuth(undefined, false)).not.toThrow();
});
