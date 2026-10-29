import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { ariaKeys, keyLabel, matchNumber, matchShortcut, SHORTCUTS, type ShortcutId } from "./shortcuts";

const all = new Set(SHORTCUTS.map((s) => s.id));

function key(init: KeyboardEventInit, target?: HTMLElement): KeyboardEvent {
  const e = new KeyboardEvent("keydown", { bubbles: true, cancelable: true, ...init });
  if (target) {
    document.body.append(target);
    Object.defineProperty(e, "target", { value: target });
  }
  return e;
}

describe("matchShortcut", () => {
  it.each<[KeyboardEventInit, ShortcutId]>([
    [{ key: "?" , shiftKey: true }, "help"],
    [{ key: "/" }, "search"],
    [{ key: "j" }, "next"],
    [{ key: "k" }, "prev"],
    [{ key: "Enter" }, "edit"],
    [{ key: "Escape" }, "leave"],
    [{ key: "Enter", metaKey: true }, "save"],
    [{ key: "Enter", ctrlKey: true }, "save"],
    [{ key: "Enter", ctrlKey: true, shiftKey: true }, "saveApprove"],
    [{ key: "Enter", metaKey: true, shiftKey: true }, "saveApprove"],
    [{ key: "p" }, "publish"],
    [{ key: "i" }, "importFile"],
    [{ key: "x" }, "exportFile"],
  ])("%j → %s", (init, id) => {
    expect(matchShortcut(key(init), all)?.id).toBe(id);
  });

  it("ignores plain keys while typing but keeps chords", () => {
    const area = document.createElement("textarea");
    expect(matchShortcut(key({ key: "j" }, area), all)).toBeUndefined();
    expect(matchShortcut(key({ key: "/" }, area), all)).toBeUndefined();
    expect(matchShortcut(key({ key: "Enter" }, area), all)).toBeUndefined();
    expect(matchShortcut(key({ key: "Enter", ctrlKey: true }, area), all)?.id).toBe("save");
    expect(matchShortcut(key({ key: "Escape" }, area), all)?.id).toBe("leave");
  });

  it("leaves Enter to links and buttons", () => {
    const button = document.createElement("button");
    const link = document.createElement("a");
    link.href = "/x";
    expect(matchShortcut(key({ key: "Enter" }, button), all)).toBeUndefined();
    expect(matchShortcut(key({ key: "Enter" }, link), all)).toBeUndefined();
    expect(matchShortcut(key({ key: "j" }, link), all)?.id).toBe("next");
  });

  it("treats checkboxes as not typing", () => {
    const box = document.createElement("input");
    box.type = "checkbox";
    expect(matchShortcut(key({ key: "j" }, box), all)?.id).toBe("next");
  });

  it("only matches enabled shortcuts and never during IME composition", () => {
    expect(matchShortcut(key({ key: "j" }), new Set(["help"]))).toBeUndefined();
    expect(matchShortcut(key({ key: "j", isComposing: true }), all)).toBeUndefined();
    expect(matchShortcut(key({ key: "j", ctrlKey: true }), all)).toBeUndefined();
  });
});

describe("knowledge and AI chords", () => {
  const workspace = new Set<ShortcutId>(["next", "prev", "save", "saveApprove", "insertMatch", "editSuggestion", "acceptSuggestion"]);
  const queue = new Set<ShortcutId>(["queueNext", "queuePrev", "queueAccept", "queueEdit", "queueReject", "queueAcceptEdit", "queueCancelEdit"]);
  /** A chord as browsers report it: AltGr is its own modifier (happy-dom folds it into Alt). */
  const chord = (init: KeyboardEventInit, target?: HTMLElement, altGr = false) => {
    const e = key(init, target);
    Object.defineProperty(e, "getModifierState", { value: (k: string) => (k === "AltGraph" ? altGr : false) });
    return e;
  };

  it("inserts TM matches by physical digit, also while typing", () => {
    const area = document.createElement("textarea");
    // On macOS ⌥ changes e.key ("¡"); the chord goes by e.code.
    const e = chord({ key: "¡", code: "Digit1", metaKey: true, altKey: true }, area);
    expect(matchShortcut(e, workspace)?.id).toBe("insertMatch");
    expect(matchNumber(e)).toBe(1);
    expect(matchShortcut(chord({ key: "9", code: "Digit9", ctrlKey: true, altKey: true }), workspace)?.id).toBe("insertMatch");
    expect(matchShortcut(chord({ key: "0", code: "Digit0", ctrlKey: true, altKey: true }), workspace)?.id).toBe("editSuggestion");
    expect(matchShortcut(chord({ key: "Enter", ctrlKey: true, altKey: true }), workspace)?.id).toBe("acceptSuggestion");
    expect(matchShortcut(chord({ key: "Enter", ctrlKey: true }), workspace)?.id).toBe("save");
  });

  it("never fires for AltGr, which types characters on many layouts", () => {
    expect(matchShortcut(chord({ key: "{", code: "Digit7", ctrlKey: true, altKey: true }, undefined, true), workspace)).toBeUndefined();
  });

  it("triages the review queue with single keys, but not while editing", () => {
    for (const [k, id] of [["a", "queueAccept"], ["e", "queueEdit"], ["r", "queueReject"], ["j", "queueNext"], ["k", "queuePrev"]] as const) {
      expect(matchShortcut(key({ key: k }), queue)?.id).toBe(id);
    }
    const area = document.createElement("textarea");
    expect(matchShortcut(key({ key: "a" }, area), queue)).toBeUndefined();
    expect(matchShortcut(key({ key: "Enter", metaKey: true }, area), queue)?.id).toBe("queueAcceptEdit");
    expect(matchShortcut(key({ key: "Escape" }, area), queue)?.id).toBe("queueCancelEdit");
  });

  it("names chords for aria-keyshortcuts", () => {
    expect(ariaKeys(["Mod", "Alt", "1"], true)).toBe("Meta+Alt+1");
    expect(ariaKeys(["Mod", "Enter"], false)).toBe("Control+Enter");
  });
});

describe("keyLabel", () => {
  it("renders per platform", () => {
    expect(keyLabel("Mod", true)).toBe("⌘");
    expect(keyLabel("Mod", false)).toBe("Ctrl");
    expect(keyLabel("j", false)).toBe("j");
  });
});

describe("documentation", () => {
  it("lists every shortcut in studio/README.md", () => {
    const readme = readFileSync(resolve(process.cwd(), "README.md"), "utf8");
    for (const s of SHORTCUTS) expect(readme, `README lacks "${s.description}"`).toContain(s.description);
  });
});
