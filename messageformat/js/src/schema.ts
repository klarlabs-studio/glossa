/**
 * Runtime validation of the canonical data model with zod. The schemas mirror
 * `message.schema.json` one to one; the test suite proves they accept and
 * reject the same values as the JSON Schema.
 */
import { z } from "zod";
import type { Message } from "./model.js";

export const literalSchema = z.object({ type: z.literal("literal"), value: z.string() });

export const variableRefSchema = z.object({ type: z.literal("variable"), name: z.string() });

const literalOrVariable = z.discriminatedUnion("type", [literalSchema, variableRefSchema]);

export const optionsSchema = z.record(z.string(), literalOrVariable);

export const attributesSchema = z.record(z.string(), z.union([literalSchema, z.literal(true)]));

export const functionRefSchema = z.object({
  type: z.literal("function"),
  name: z.string(),
  options: optionsSchema.optional(),
});

const expressionShape = {
  type: z.literal("expression"),
  arg: literalOrVariable.optional(),
  function: functionRefSchema.optional(),
  attributes: attributesSchema.optional(),
};

const hasArgOrFunction = (e: { arg?: unknown; function?: unknown }) =>
  e.arg !== undefined || e.function !== undefined;

export const expressionSchema = z
  .object(expressionShape)
  .refine(hasArgOrFunction, { message: "An expression needs an arg, a function, or both" });

export const markupSchema = z.object({
  type: z.literal("markup"),
  kind: z.enum(["open", "standalone", "close"]),
  name: z.string(),
  options: optionsSchema.optional(),
  attributes: attributesSchema.optional(),
});

export const patternSchema = z.array(z.union([z.string(), expressionSchema, markupSchema]));

export const declarationSchema = z.discriminatedUnion("type", [
  z.object({
    type: z.literal("input"),
    name: z.string(),
    value: z.object({ ...expressionShape, arg: variableRefSchema }),
  }),
  z.object({ type: z.literal("local"), name: z.string(), value: expressionSchema }),
]);

export const variantKeySchema = z.discriminatedUnion("type", [
  literalSchema,
  z.object({ type: z.literal("*"), value: z.string().optional() }),
]);

export const patternMessageSchema = z.object({
  type: z.literal("message"),
  declarations: z.array(declarationSchema),
  pattern: patternSchema,
});

export const selectMessageSchema = z.object({
  type: z.literal("select"),
  declarations: z.array(declarationSchema),
  selectors: z.array(variableRefSchema),
  variants: z.array(z.object({ keys: z.array(variantKeySchema), value: patternSchema })),
});

/** The canonical MessageFormat 2 message, as carried by release artifacts. */
export const messageSchema: z.ZodType<Message> = z.discriminatedUnion("type", [
  patternMessageSchema,
  selectMessageSchema,
]) as z.ZodType<Message>;

/** Type guard: `value` is a well-formed canonical message. */
export const isMessage = (value: unknown): value is Message =>
  messageSchema.safeParse(value).success;
