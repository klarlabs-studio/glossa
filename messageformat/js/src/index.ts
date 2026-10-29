export type {
  Attributes,
  CatchallKey,
  Declaration,
  Expression,
  FunctionRef,
  InputDeclaration,
  Literal,
  LocalDeclaration,
  Markup,
  Message,
  Options,
  Pattern,
  PatternMessage,
  SelectMessage,
  VariableRef,
  Variant,
} from "./model.js";

export {
  MessageDataModelError,
  MessageSyntaxError,
  format,
  formatToParts,
  parseMF2,
  stringify,
  validateMessage,
} from "./mf2.js";
export type { FormatError, FormatOptions, MessageFunction, MessagePart } from "./mf2.js";

export { fromReference, toReference } from "./convert.js";

export {
  attributesSchema,
  declarationSchema,
  expressionSchema,
  functionRefSchema,
  isMessage,
  literalSchema,
  markupSchema,
  messageSchema,
  optionsSchema,
  patternMessageSchema,
  patternSchema,
  selectMessageSchema,
  variableRefSchema,
  variantKeySchema,
} from "./schema.js";
