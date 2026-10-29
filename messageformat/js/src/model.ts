/**
 * The canonical MessageFormat 2 data model, exactly as the spec's JSON Schema
 * (`messageformat/testdata/unicode/data-model/message.schema.json`) describes it.
 *
 * This is the wire contract: release artifacts carry messages in this shape,
 * and every runtime reads this shape. Values are plain JSON: options and
 * attributes are objects, never `Map`s.
 */

export type Message = PatternMessage | SelectMessage;

export interface PatternMessage {
  type: "message";
  declarations: Declaration[];
  pattern: Pattern;
}

export interface SelectMessage {
  type: "select";
  declarations: Declaration[];
  selectors: VariableRef[];
  variants: Variant[];
}

export type Declaration = InputDeclaration | LocalDeclaration;

export interface InputDeclaration {
  type: "input";
  name: string;
  value: Expression & { arg: VariableRef };
}

export interface LocalDeclaration {
  type: "local";
  name: string;
  value: Expression;
}

export interface Variant {
  keys: Array<Literal | CatchallKey>;
  value: Pattern;
}

export interface CatchallKey {
  type: "*";
  value?: string;
}

export type Pattern = Array<string | Expression | Markup>;

export interface Expression {
  type: "expression";
  arg?: Literal | VariableRef;
  function?: FunctionRef;
  attributes?: Attributes;
}

export interface Literal {
  type: "literal";
  value: string;
}

export interface VariableRef {
  type: "variable";
  name: string;
}

export interface FunctionRef {
  type: "function";
  name: string;
  options?: Options;
}

export interface Markup {
  type: "markup";
  kind: "open" | "standalone" | "close";
  name: string;
  options?: Options;
  attributes?: Attributes;
}

export type Options = Record<string, Literal | VariableRef>;

export type Attributes = Record<string, Literal | true>;
