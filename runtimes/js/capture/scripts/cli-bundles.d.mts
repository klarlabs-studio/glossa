export interface Bundle {
  path: string;
  what: string;
  /** An entry module's source, or… */
  contents?: string;
  /** …an entry file. */
  entry?: string;
}
export const bundles: Bundle[];
export function bundle(b: Bundle): Promise<string>;
export function checkedIn(b: Bundle): Promise<string>;
