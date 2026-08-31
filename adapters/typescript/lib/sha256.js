'use strict';

const crypto = require('crypto');
const { countPrecedingBackslashes, isRegexStart } = require('./scanner');

/**
 * Computes the lowercase hex SHA-256 digest of a string or buffer.
 * @param {string|Buffer} input
 * @returns {string} 64-character lowercase hex digest
 */
function sha256Hex(input) {
  return crypto.createHash('sha256').update(input).digest('hex');
}

/**
 * Computes the canonical whitespace/comment-invariant AST fingerprint.
 * Preserves strings (including URLs like https://...) and regexes while stripping comments.
 * @param {string} nodeText
 * @returns {string} 64-character lowercase hex digest
 */
function canonicalAstFingerprint(nodeText) {
  let result = '';
  let inString = false;
  let stringChar = '';
  let inComment = false;

  for (let i = 0; i < nodeText.length; i++) {
    const ch = nodeText[i];

    if (inComment === 'line') {
      if (ch === '\n') {
        inComment = false;
        result += ' ';
      }
      continue;
    }

    if (inComment === 'block') {
      if (ch === '*' && nodeText[i + 1] === '/') {
        inComment = false;
        i++;
        result += ' ';
      }
      continue;
    }

    if (inString) {
      result += ch;
      if (ch === stringChar) {
        const backslashes = countPrecedingBackslashes(nodeText, i);
        if (backslashes % 2 === 0) {
          inString = false;
          stringChar = '';
        }
      }
      continue;
    }

    if (ch === '/' && nodeText[i + 1] === '/') {
      inComment = 'line';
      i++;
      continue;
    }

    if (ch === '/' && nodeText[i + 1] === '*') {
      inComment = 'block';
      i++;
      continue;
    }

    if (ch === '"' || ch === "'" || ch === '`') {
      inString = true;
      stringChar = ch;
      result += ch;
      continue;
    }

    if (ch === '/' && isRegexStart(nodeText, i)) {
      result += ch;
      let j = i + 1;
      let inCharClass = false;
      while (j < nodeText.length) {
        const c = nodeText[j];
        result += c;
        if (c === '\n') break;
        if (c === '\\') {
          if (j + 1 < nodeText.length) {
            result += nodeText[j + 1];
            j += 2;
            continue;
          }
        }
        if (c === '[') inCharClass = true;
        else if (c === ']' && inCharClass) inCharClass = false;
        else if (c === '/' && !inCharClass) {
          j++;
          while (j < nodeText.length && /[a-z]/i.test(nodeText[j])) {
            result += nodeText[j];
            j++;
          }
          i = j - 1;
          break;
        }
        j++;
      }
      continue;
    }

    result += ch;
  }

  const norm = result.replace(/\s+/g, ' ').trim();
  return sha256Hex(norm);
}

/**
 * Computes UTF-8 byte offset for a character index.
 * @param {string} source
 * @param {number} charOffset
 * @returns {number}
 */
function byteOffset(source, charOffset) {
  if (charOffset <= 0) return 0;
  if (charOffset >= source.length) return Buffer.byteLength(source, 'utf8');
  return Buffer.byteLength(source.substring(0, charOffset), 'utf8');
}

module.exports = {
  sha256Hex,
  canonicalAstFingerprint,
  byteOffset,
};
