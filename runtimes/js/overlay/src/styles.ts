/**
 * The panel's styles. Lit adopts them as a constructable stylesheet into the
 * shadow root, so a preview deployment's CSP needs no `unsafe-inline`. The
 * host resets inherited styles so the page can't restyle the panel, and
 * every foreground/background pair meets WCAG AA (4.5:1 for text, 3:1 for
 * focus rings and borders of controls).
 */
import { css } from "lit";

export const styles = css`
  :host {
    all: initial;
    --ink: #1a1a1a; /* 17.4:1 on white */
    --muted: #4a4a4a; /* 8.9:1 */
    --line: #767676; /* 4.5:1, control borders */
    --paper: #ffffff;
    --wash: #f3f4f6; /* ink on wash 15.9:1 */
    --accent: #1d4ed8; /* 6.7:1 on white; white on it 6.7:1 */
    --danger: #b42318; /* 6.2:1 */
    --ok: #067647; /* 5.6:1 */
    --warn: #8a4b00; /* 6.6:1 */
  }
  [role="dialog"] {
    position: fixed;
    inset-block: 0;
    inset-inline-end: 0;
    z-index: 2147483647;
    box-sizing: border-box;
    width: min(26rem, 100vw);
    overflow-y: auto;
    padding: 1rem 1.25rem 1.5rem;
    background: var(--paper);
    color: var(--ink);
    border-inline-start: 1px solid var(--line);
    box-shadow: -0.5rem 0 1.5rem rgb(0 0 0 / 0.18);
    font:
      14px/1.45 system-ui,
      -apple-system,
      "Segoe UI",
      Roboto,
      sans-serif;
    text-align: start;
  }
  header {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 0.75rem;
  }
  h2 {
    margin: 0;
    font-size: 1.05rem;
    overflow-wrap: anywhere;
  }
  h3 {
    margin: 1rem 0 0.25rem;
    font-size: 0.85rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--muted);
  }
  p {
    margin: 0.25rem 0;
  }
  code,
  .text {
    font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    font-size: 0.9em;
  }
  .text {
    display: block;
    padding: 0.5rem;
    background: var(--wash);
    border-radius: 4px;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }
  .meta {
    color: var(--muted);
    font-size: 0.85rem;
  }
  label {
    display: block;
    margin: 0.75rem 0 0.25rem;
    font-weight: 600;
  }
  textarea,
  select {
    box-sizing: border-box;
    width: 100%;
    font: inherit;
    color: var(--ink);
    background: var(--paper);
    border: 1px solid var(--line);
    border-radius: 4px;
    padding: 0.4rem 0.5rem;
  }
  textarea {
    min-height: 6rem;
    resize: vertical;
    font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  }
  textarea[aria-invalid="true"] {
    border-color: var(--danger);
    border-width: 2px;
  }
  .row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem;
    margin-top: 0.75rem;
  }
  button,
  a.button {
    font: inherit;
    cursor: pointer;
    border-radius: 4px;
    padding: 0.4rem 0.8rem;
    border: 1px solid var(--accent);
    background: var(--paper);
    color: var(--accent);
    text-decoration: none;
  }
  button.primary {
    background: var(--accent);
    color: #ffffff;
  }
  button[disabled] {
    cursor: not-allowed;
    border-color: var(--line);
    color: var(--muted);
    background: var(--wash);
  }
  button.close {
    border-color: transparent;
    color: var(--ink);
    padding: 0.2rem 0.5rem;
    font-size: 1.1rem;
    line-height: 1;
  }
  button.disclosure {
    display: flex;
    width: 100%;
    justify-content: space-between;
    margin-top: 0.75rem;
    border-color: var(--line);
    color: var(--ink);
  }
  :focus-visible {
    outline: 3px solid var(--accent);
    outline-offset: 2px;
  }
  ul,
  ol {
    margin: 0.25rem 0;
    padding-inline-start: 1.25rem;
  }
  li {
    margin: 0.35rem 0;
  }
  .error {
    color: var(--danger);
  }
  .ok {
    color: var(--ok);
  }
  .warn {
    color: var(--warn);
  }
  .notice {
    margin-top: 0.75rem;
    padding: 0.5rem 0.75rem;
    border-radius: 4px;
    border: 1px solid currentColor;
    background: var(--paper);
  }
  .badge {
    display: inline-block;
    padding: 0 0.4rem;
    border: 1px solid currentColor;
    border-radius: 999px;
    font-size: 0.8rem;
  }
  .visually-hidden {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
  }
`;
