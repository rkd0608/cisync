// Draft 2020-12 support lives in the dedicated ajv entrypoint (same package).
import Ajv2020, { type ValidateFunction } from 'ajv/dist/2020';
import contractSchema from '../../../packages/contracts/events.schema.json';

/**
 * Ajv compilation of packages/contracts/events.schema.json (draft 2020-12).
 * WHY two layers: the document's top-level oneOf validates only the generic
 * Envelope; the normative per-type payload rules live under
 * $defs/CORE_EVENTS keyed by event type and must be composed explicitly.
 * The date-time format is registered natively to avoid adding an
 * unapproved dependency.
 */

const SCHEMA_ID = 'https://cisync.dev/contracts/events.schema.json';
const DATE_TIME_RE = /^\d{4}-\d{2}-\d{2}[Tt]\d{2}:\d{2}:\d{2}(\.\d+)?([Zz]|[+-]\d{2}:\d{2})$/;

let fullValidate: ValidateFunction | undefined;
const typedValidators = new Map<string, ValidateFunction>();

function compiler(): Ajv2020 {
  const ajv = new Ajv2020({ strict: false, allErrors: true });
  ajv.addFormat('date-time', DATE_TIME_RE);
  ajv.addSchema(contractSchema);
  return ajv;
}

export interface SchemaVerdict {
  valid: boolean;
  errors: string[];
}

/** Generic envelope structure (the document's own top-level oneOf). */
export function validateAgainstEventSchema(candidate: unknown): SchemaVerdict {
  if (!fullValidate) {
    const ajv = compiler();
    fullValidate = ajv.compile({ $ref: `${SCHEMA_ID}#/$defs/Envelope` });
  }
  return verdictFor(fullValidate, candidate);
}

/** Envelope of one specific event type: Envelope rules PLUS the matching
 *  CORE_EVENTS payload definition PLUS type agreement.
 *  WHY re-declare $defs: the extracted CORE payload subschema uses relative
 *  $refs; hosting them under a wrapper with identical $defs keeps those
 *  pointers resolvable without cross-document id juggling. */
export function envelopeForType(type: string, candidate: unknown): SchemaVerdict {
  let validate = typedValidators.get(type);
  if (!validate) {
    const core = corePayloadDefinition(type);
    if (!core) return { valid: false, errors: [`event type "${type}" is not part of the frozen CORE set`] };
    const ajv = compiler();
    const wrapper = {
      $defs: (contractSchema as { $defs?: Record<string, unknown> })['$defs'],
      allOf: [
        { $ref: '#/$defs/Envelope' },
        {
          type: 'object',
          required: ['type', 'payload'],
          properties: { type: { const: type }, payload: core },
        },
      ],
    };
    validate = ajv.compile(wrapper);
    typedValidators.set(type, validate);
  }
  return verdictFor(validate, candidate);
}

function verdictFor(validate: ValidateFunction, candidate: unknown): SchemaVerdict {
  const ok = validate(candidate) as boolean;
  return {
    valid: ok,
    errors: ok ? [] : (validate.errors ?? []).map((e) => `${e.instancePath || '/'} ${e.message ?? ''}`.trim()),
  };
}

/** CORE payload definition for one event type, or undefined if unknown. */
export function corePayloadDefinition(type: string): unknown {
  const defs = (contractSchema as { $defs?: Record<string, unknown> })['$defs'];
  const core = defs?.['CORE_EVENTS'] as { properties?: Record<string, unknown> } | undefined;
  return core?.properties?.[type];
}
