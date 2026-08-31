'use strict';

/**
 * Splits an identifier on camelCase, snake_case, underscores, and dollar signs.
 * Handles acronym runs properly: e.g. "URLLoader" -> ["URL", "Loader"].
 * @param {string} raw
 * @returns {string[]}
 */
function splitIdentifierWords(raw) {
  let s = String(raw || '').trim().replace(/^_+/, '');
  // Drop leading "on" prefix when followed by an uppercase letter: onItemAdded -> ItemAdded
  if (s.length > 2 && s.startsWith('on') && s[2] >= 'A' && s[2] <= 'Z') {
    s = s.substring(2);
  }

  const words = [];
  let current = '';

  for (let i = 0; i < s.length; i++) {
    const ch = s[i];
    if (ch === '_' || ch === '$') {
      if (current.length > 0) {
        words.push(current);
        current = '';
      }
      continue;
    }

    const isUpper = ch >= 'A' && ch <= 'Z';
    const isDigit = ch >= '0' && ch <= '9';
    const prevLowerOrDigit = i > 0 && ((s[i - 1] >= 'a' && s[i - 1] <= 'z') || (s[i - 1] >= '0' && s[i - 1] <= '9'));
    const isAcronymEnd = current.length >= 2 && s[i - 1] >= 'A' && s[i - 1] <= 'Z' && i + 1 < s.length && s[i + 1] >= 'a' && s[i + 1] <= 'z';

    if (isUpper && (prevLowerOrDigit || isAcronymEnd)) {
      if (current.length > 0) {
        words.push(current);
        current = '';
      }
    }
    current += ch;
  }

  if (current.length > 0) {
    words.push(current);
  }

  return words;
}

/**
 * Humanizes an identifier into a clean English sentence fragment.
 * e.g. "submitOrder" -> "Submit order"
 * @param {string} rawName
 * @returns {string}
 */
function humanizeIdentifier(rawName) {
  const words = splitIdentifierWords(rawName)
    .map(w => w.toLowerCase())
    .filter(w => w.length > 0);

  if (words.length === 0) {
    return 'Unnamed';
  }

  const first = words[0];
  const capitalizedFirst = first.charAt(0).toUpperCase() + first.slice(1);
  words[0] = capitalizedFirst;

  return words.join(' ');
}

module.exports = {
  splitIdentifierWords,
  humanizeIdentifier,
};
