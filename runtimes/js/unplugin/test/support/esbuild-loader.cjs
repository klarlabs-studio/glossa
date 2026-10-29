// A minimal webpack loader for the fixture harness: esbuild strips TS and
// JSX and hands the next loader (the plugin's post loader) its source map.
const { extname } = require("node:path");
const esbuild = require("esbuild");

const LOADERS = { ".ts": "ts", ".tsx": "tsx", ".js": "js", ".jsx": "jsx" };

module.exports = function esbuildLoader(source) {
  const done = this.async();
  esbuild
    .transform(source, {
      loader: LOADERS[extname(this.resourcePath)] ?? "js",
      sourcemap: "external",
      sourcefile: this.resourcePath,
      jsx: "automatic",
      format: "esm",
    })
    .then((out) => done(null, out.code, JSON.parse(out.map)), done);
};
