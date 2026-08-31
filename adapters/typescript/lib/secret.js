'use strict';

// Single-gate secret pattern matching internal/secret (R5, A6)
const secretPattern = /\b(?:api[_-]?key|secret|token|password)\s*(?:===|==|:=|[:=])\s*['"]?[^\s;'"]{3,}['"]?/gi;

/**
 * Replaces secret tokens matching standard key/token/password patterns with "***REDACTED***".
 * @param {string} input
 * @returns {{ text: string, count: number }}
 */
function redactSecrets(input) {
  if (!input || typeof input !== 'string') {
    return { text: input || '', count: 0 };
  }
  let count = 0;
  const text = input.replace(secretPattern, (match) => {
    count++;
    const opMatch = match.match(/^(.*?\b(?:api[_-]?key|secret|token|password)\s*(?:===|==|:=|[:=]))\s*/i);
    if (opMatch) {
      return `${opMatch[1]} "***REDACTED***"`;
    }
    return '***REDACTED***';
  });
  return { text, count };
}

module.exports = {
  redactSecrets,
  secretPattern,
};
