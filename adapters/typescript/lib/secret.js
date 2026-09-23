'use strict';

// Single-gate secret pattern matching internal/secret (R5, A6)
const secretPattern = /(?:(?:\b(?:api[_-]?key|secret|token|password|credential|authorization|private[_-]?key|access[_-]?token|client[_-]?secret)\b)|[A-Za-z][A-Za-z0-9]*(?:api[_-]?key|secret|token|password|credential|authorization|private[_-]?key|access[_-]?token|client[_-]?secret))\s*(?:===|==|:=|[:=])\s*['"]?[^\s;'"}]+['"]?/gi;
const quotedSecretPattern = /"(?:(?:api[_-]?key|secret|token|password|credential|authorization|private[_-]?key|access[_-]?token|client[_-]?secret)[A-Za-z0-9_-]*|[A-Za-z][A-Za-z0-9_-]*(?:api[_-]?key|secret|token|password|credential|authorization|private[_-]?key|access[_-]?token|client[_-]?secret)[A-Za-z0-9_-]*)"\s*:\s*(?:"[^"\\]*(?:\\.[^"\\]*)*"?|[^,\s}\]]+)/gis;
const sensitiveAssignmentPattern = /((?:\b(?:const|let|var)\s+|^\s*)\b(?:api[_-]?key|secret|token|password|credential|authorization|private[_-]?key|access[_-]?token|client[_-]?secret)\b\s*(?::=|:(?!=)|=(?!=|>))\s*)([^;\r\n]+)/gim;

function redactRaw(input) {
  let count = 0;
  let text = input.replace(quotedSecretPattern, () => {
    count++;
    return '***REDACTED***';
  });
  // A sensitive variable can receive its value through a call such as
  // request.headers.get(...). Redact the complete expression, not only its
  // first token, so the remaining display text cannot expose a header name or
  // become a broken source fragment.
  text = text.replace(sensitiveAssignmentPattern, (_, prefix) => {
    count++;
    return `${prefix}"***REDACTED***"`;
  });
  text = text.replace(secretPattern, (match) => {
    if (match.includes('***REDACTED***')) return match;
    count++;
    const opMatch = match.match(/^(.*?\b(?:api[_-]?key|secret|token|password|credential|authorization|private[_-]?key|access[_-]?token|client[_-]?secret)\s*(?:===|==|:=|[:=]))\s*/i);
    if (opMatch) return `${opMatch[1]} "***REDACTED***"`;
    return '***REDACTED***';
  });
  return { text, count };
}

function sensitiveKey(raw) {
  const key = String(raw || '').toLowerCase().trim();
  if (!/(?:api[_-]?key|secret|token|password|credential|authorization|private[_-]?key|access[_-]?token|client[_-]?secret)/i.test(key)) return false;
  const compact = key.replace(/[-_\s]/g, '');
  return ['apikey', 'secret', 'token', 'password', 'credential', 'authorization', 'privatekey', 'accesskey', 'accesstoken', 'clientsecret']
    .some((marker) => compact === marker || compact.startsWith(marker) || compact.endsWith(marker));
}

function sanitizeValue(value) {
  if (typeof value === 'string') {
    const clean = redactRaw(value);
    return { value: clean.text, count: clean.count };
  }
  if (Array.isArray(value)) {
    let count = 0;
    const out = value.map((item) => {
      const clean = sanitizeValue(item);
      count += clean.count;
      return clean.value;
    });
    return { value: out, count };
  }
  if (value && typeof value === 'object') {
    let count = 0;
    const out = {};
    for (const [key, item] of Object.entries(value)) {
      if (sensitiveKey(key)) {
        count++;
        out[key] = '***REDACTED***';
        continue;
      }
      const clean = sanitizeValue(item);
      count += clean.count;
      out[key] = clean.value;
    }
    return { value: out, count };
  }
  return { value, count: 0 };
}

/**
 * Replaces secret tokens matching standard key/token/password patterns with "***REDACTED***".
 * @param {string} input
 * @returns {{ text: string, count: number }}
 */
function redactSecrets(input) {
  if (!input || typeof input !== 'string') {
    return { text: input || '', count: 0 };
  }
  const trimmed = input.trim();
  if (trimmed.startsWith('{') || trimmed.startsWith('[')) {
    try {
      const clean = sanitizeValue(JSON.parse(input));
      return { text: JSON.stringify(clean.value), count: clean.count };
    } catch (_) {
      // Malformed JSON is handled by the raw scanner below.
    }
  }
  return redactRaw(input);
}

function redactDiagnostic(value, maxBytes = 512, maxItems = 64) {
  const clean = typeof value === 'string' ? redactSecrets(value) : sanitizeValue(value);
  let safe = typeof clean.value === 'undefined' ? clean.text : clean.value;
  if (typeof safe !== 'string') {
    if (Array.isArray(safe) && maxItems > 0) safe = safe.slice(0, maxItems);
    safe = JSON.stringify(safe);
  }
  if (maxBytes > 0 && Buffer.byteLength(safe, 'utf8') > maxBytes) {
    safe = Buffer.from(safe, 'utf8').subarray(0, maxBytes).toString('utf8');
  }
  return safe;
}

module.exports = {
  redactSecrets,
  redactDiagnostic,
  secretPattern,
};
