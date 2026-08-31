'use strict';

const fs = require('fs');
const path = require('path');

const GENERIC_PARAMS = '<(?:[^<>]|<(?:[^<>]|<[^<>]*>)*>)*>';
const NESTED_PARENS = '\\((?:[^()]|\\([^()]*\\))*\\)';

const importRegex = /(?:import\s+(?:(?:(?:\*\s+as\s+[A-Za-z0-9_$]+)|(?:\{[^}]+\})|(?:[A-Za-z0-9_$]+))\s+from\s+)?['"]([^'"]+)['"])|(?:const\s+(?:\{[^}]+\}|[A-Za-z0-9_$]+)\s*=\s*require\(['"]([^'"]+)['"]\))/g;
const classRegex = new RegExp(`(?:export\\s+)?(?:default\\s+)?(?:abstract\\s+)?class\\s+([A-Za-z0-9_$]+)\\s*(?:${GENERIC_PARAMS})?(?:\\s+extends\\s+([A-Za-z0-9_$]+)(?:${GENERIC_PARAMS})?)?(?:\\s+implements\\s+([^{]+))?\\s*\\{`, 'g');
const fnRegex = new RegExp(`(?:export\\s+)?(?:default\\s+)?(?:async\\s+)?function\\s*(?:\\*\\s*)?([A-Za-z0-9_$]+)\\s*(?:${GENERIC_PARAMS})?\\s*${NESTED_PARENS}\\s*(?::\\s*[^{]+)?\\s*\\{`, 'g');
const arrowRegex = new RegExp(`(?:export\\s+)?(?:const|let|var)\\s+([A-Za-z0-9_$]+)\\s*(?::\\s*[^=]+)?\\s*=\\s*(?:async\\s+)?(?:${GENERIC_PARAMS})?\\s*(?:${NESTED_PARENS}|[A-Za-z0-9_$]+)\\s*(?::\\s*[^{=]*?)?\\s*=>\\s*\\{`, 'g');
const methodRegex = new RegExp(`(?:public\\s+|private\\s+|protected\\s+|static\\s+|async\\s+|override\\s+|readonly\\s+)*(?:get\\s+|set\\s+)?([A-Za-z0-9_$]+)\\s*(?:${GENERIC_PARAMS})?\\s*${NESTED_PARENS}\\s*(?::\\s*[^{]+)?\\s*\\{`, 'g');

/**
 * Counts consecutive preceding backslashes immediately before index.
 * @param {string} str
 * @param {number} index
 * @returns {number}
 */
function countPrecedingBackslashes(str, index) {
  let count = 0;
  let j = index - 1;
  while (j >= 0 && str[j] === '\\') {
    count++;
    j--;
  }
  return count;
}

/**
 * Checks if a slash character at slashIndex starts a regex literal.
 * @param {string} str
 * @param {number} slashIndex
 * @returns {boolean}
 */
function isRegexStart(str, slashIndex) {
  let j = slashIndex - 1;
  while (j >= 0 && /\s/.test(str[j])) j--;
  if (j < 0) return true; // Start of string
  const ch = str[j];
  if ('=([{:;,!?&|+-*%^~<>'.includes(ch)) return true;

  let wordEnd = j + 1;
  let wordStart = j;
  while (wordStart >= 0 && /[A-Za-z0-9_$]/.test(str[wordStart])) wordStart--;
  const word = str.substring(wordStart + 1, wordEnd);
  const exprKeywords = new Set([
    'return', 'typeof', 'void', 'delete', 'await', 'yield', 'throw',
    'case', 'default', 'do', 'else', 'in', 'instanceof', 'of'
  ]);
  return exprKeywords.has(word);
}

/**
 * Scans a JS/TS source file to extract declarations, imports, classes, and methods.
 * @param {string} source
 * @returns {object}
 */
function scanSource(source) {
  const classes = [];
  const topLevelFunctions = [];
  const imports = [];

  // 1. Extract import statements
  const impRe = new RegExp(importRegex.source, 'g');
  let imp;
  while ((imp = impRe.exec(source)) !== null) {
    const importPath = imp[1] || imp[2];
    if (importPath) {
      imports.push(importPath);
    }
  }

  // 2. Extract class declarations and their methods
  const clsRe = new RegExp(classRegex.source, 'g');
  let clsMatch;
  while ((clsMatch = clsRe.exec(source)) !== null) {
    const className = clsMatch[1];
    const extendsName = clsMatch[2] || null;
    const bodyStart = clsMatch.index + clsMatch[0].length;
    const bodyEnd = findMatchingBrace(source, bodyStart - 1);
    const resolvedEnd = bodyEnd >= 0 ? bodyEnd : source.length;
    const classBody = source.substring(bodyStart, resolvedEnd);

    const methods = scanMethods(classBody, bodyStart);
    classes.push({
      name: className,
      extendsName,
      bodyStart,
      bodyEnd: resolvedEnd,
      methods,
    });

    if (bodyEnd >= 0) {
      clsRe.lastIndex = bodyEnd + 1;
    }
  }

  function isInsideClass(pos) {
    for (const cls of classes) {
      if (pos >= cls.bodyStart && pos <= cls.bodyEnd) {
        return true;
      }
    }
    return false;
  }

  // 3. Extract top-level function declarations
  const fnRe = new RegExp(fnRegex.source, 'g');
  let fnMatch;
  while ((fnMatch = fnRe.exec(source)) !== null) {
    if (isInsideClass(fnMatch.index)) {
      continue;
    }
    const fnName = fnMatch[1];
    const bodyStart = fnMatch.index + fnMatch[0].length;
    const bodyEnd = findMatchingBrace(source, bodyStart - 1);
    topLevelFunctions.push({
      name: fnName,
      bodyStart,
      bodyEnd: bodyEnd >= 0 ? bodyEnd : source.length,
      isAsync: fnMatch[0].includes('async'),
    });

    if (bodyEnd >= 0) {
      fnRe.lastIndex = bodyEnd + 1;
    }
  }

  // 4. Extract const arrow functions (including handlers inside React components)
  const arrowRe = new RegExp(arrowRegex.source, 'g');
  let arrowMatch;
  while ((arrowMatch = arrowRe.exec(source)) !== null) {
    if (isInsideClass(arrowMatch.index)) {
      continue;
    }
    const fnName = arrowMatch[1];
    const bodyStart = arrowMatch.index + arrowMatch[0].length;
    const bodyEnd = findMatchingBrace(source, bodyStart - 1);
    topLevelFunctions.push({
      name: fnName,
      bodyStart,
      bodyEnd: bodyEnd >= 0 ? bodyEnd : source.length,
      isAsync: arrowMatch[0].includes('async'),
    });

    if (bodyEnd >= 0) {
      arrowRe.lastIndex = bodyEnd + 1;
    }
  }

  return {
    classes,
    topLevelFunctions,
    imports,
  };
}

/**
 * Scans method declarations inside a class body.
 * @param {string} classBody
 * @param {number} offset
 * @returns {Array<object>}
 */
function scanMethods(classBody, offset) {
  const methods = [];
  const re = new RegExp(methodRegex.source, 'g');
  let m;

  while ((m = re.exec(classBody)) !== null) {
    const methodName = m[1];
    if (['if', 'for', 'while', 'switch', 'catch', 'finally'].includes(methodName)) {
      continue;
    }

    const bodyStart = offset + m.index + m[0].length;
    const bodyEndInClass = findMatchingBrace(classBody, m.index + m[0].length - 1);
    const endPos = bodyEndInClass >= 0 ? offset + bodyEndInClass : offset + classBody.length;

    methods.push({
      name: methodName,
      bodyStart,
      bodyEnd: endPos,
      isAsync: m[0].includes('async'),
    });

    if (bodyEndInClass >= 0) {
      re.lastIndex = bodyEndInClass + 1;
    }
  }

  return methods;
}

/**
 * Finds the index of the matching closing brace '}' for an opening brace at openIndex.
 * @param {string} str
 * @param {number} openIndex
 * @returns {number}
 */
function findMatchingBrace(str, openIndex) {
  let depth = 0;
  let inString = false;
  let stringChar = '';
  let inComment = false;
  const templateDepthStack = [];

  for (let i = openIndex; i < str.length; i++) {
    const ch = str[i];

    if (inComment === 'line') {
      if (ch === '\n') {
        inComment = false;
      }
      continue;
    }

    if (inComment === 'block') {
      if (ch === '*' && str[i + 1] === '/') {
        inComment = false;
        i++;
      }
      continue;
    }

    if (inString) {
      if (ch === stringChar) {
        const backslashes = countPrecedingBackslashes(str, i);
        if (backslashes % 2 === 0) {
          inString = false;
          stringChar = '';
        }
      } else if (stringChar === '`' && ch === '$' && str[i + 1] === '{') {
        const backslashes = countPrecedingBackslashes(str, i);
        if (backslashes % 2 === 0) {
          templateDepthStack.push(depth);
          inString = false;
          stringChar = '';
          i++;
          depth++;
        }
      }
      continue;
    }

    if (ch === '/' && str[i + 1] === '/') {
      inComment = 'line';
      i++;
    } else if (ch === '/' && str[i + 1] === '*') {
      inComment = 'block';
      i++;
    } else if (ch === '"' || ch === "'" || ch === '`') {
      inString = true;
      stringChar = ch;
    } else if (ch === '/' && isRegexStart(str, i)) {
      let j = i + 1;
      let inCharClass = false;
      while (j < str.length) {
        const c = str[j];
        if (c === '\n') break;
        if (c === '\\') {
          j += 2;
          continue;
        }
        if (c === '[') {
          inCharClass = true;
        } else if (c === ']' && inCharClass) {
          inCharClass = false;
        } else if (c === '/' && !inCharClass) {
          j++;
          while (j < str.length && /[a-z]/i.test(str[j])) {
            j++;
          }
          i = j - 1;
          break;
        }
        j++;
      }
    } else if (ch === '{') {
      depth++;
    } else if (ch === '}') {
      depth--;
      if (templateDepthStack.length > 0 && depth === templateDepthStack[templateDepthStack.length - 1]) {
        templateDepthStack.pop();
        inString = true;
        stringChar = '`';
      } else if (depth === 0) {
        return i;
      }
    }
  }
  return -1;
}

module.exports = {
  scanSource,
  scanMethods,
  findMatchingBrace,
  countPrecedingBackslashes,
  isRegexStart,
};
