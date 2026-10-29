/** Types for m3-fixture.mjs, so src/m3-fixture.test.ts type-checks. */

/** The generated fixture application, in the platform module's testdata. */
export declare const APP: string;

/** Every generated file, by its path under the app. */
export declare const outputs: string[];

/** Builds the app's two bundles and its two usages documents into dir. */
export declare function buildFixture(dir: string): Promise<{ preview: string; branch: string }>;
