// Regex-light structural scanner for Dart source.
//
// The adapter contract forbids external packages, so there is no analyzer
// dependency here. Instead we do a conservative two-phase scan:
//
//   1. Mask phase - a small state machine walks the source and blanks out
//      comments and string literal contents (newlines preserved, offsets
//      unchanged). This removes false positives from URLs in strings,
//      commented-out code, etc. Doc comments are captured on the way.
//
//   2. Structure phase - declaration discovery over the masked text using
//      line-anchored regular expressions plus brace-depth tracking. A member
//      is only considered at brace depth 1 inside a class-like body (depth 0
//      for top-level functions), which keeps control-flow keywords and nested
//      statements out of the results.
//
// Everything produced here is deterministic for identical input bytes.
library;

/// A comment span recorded during masking (original-source offsets).
class CommentSpan {
  const CommentSpan(this.start, this.end, this.text);

  final int start;
  final int end;
  final String text;
}

/// A class/mixin/enum/extension declaration found in a file.
class ScannedClass {
  const ScannedClass({
    required this.name,
    required this.keyword,
    required this.declLine,
    required this.bodyStart,
    required this.bodyEnd,
    required this.methods,
  });

  final String name;
  final String keyword;

  /// 0-based line of the class name token.
  final int declLine;

  /// Offset of the opening `{` of the body (in source/masked coordinates).
  final int bodyStart;

  /// Offset just past the matching `}`.
  final int bodyEnd;

  /// Methods/constructors declared directly in this body (depth 1).
  final List<ScannedMethod> methods;
}

/// A method-like declaration with a resolvable body.
class ScannedMethod {
  const ScannedMethod({
    required this.name,
    required this.nameLine,
    required this.bodyStart,
    required this.bodyEnd,
  });

  final String name;

  /// 0-based line of the method name token.
  final int nameLine;

  /// Half-open source range of the body (inside the braces, or after `=>`
  /// up to the terminating `;`).
  final int bodyStart;
  final int bodyEnd;
}

/// Result of scanning one Dart file.
class ScanResult {
  const ScanResult({
    required this.source,
    required this.masked,
    required this.lines,
    required this.classes,
    required this.topLevelFunctions,
    required this.docComments,
  });

  final String source;

  /// Same length/offsets as [source]; comments and string contents blanked.
  final String masked;
  final List<String> lines;
  final List<ScannedClass> classes;
  final List<ScannedMethod> topLevelFunctions;
  final List<CommentSpan> docComments;

  static final _tripleSlash = RegExp(r'^\s*///\s?');

  /// Text of the FIRST `///` line of the contiguous doc block above
  /// [declLine] (skipping annotation lines in between), or null.
  String? firstDocLineAbove(int declLine) {
    var j = declLine - 1;
    final docs = <String>[];
    while (j >= 0) {
      final t = lines[j].trim();
      if (t.startsWith('///')) {
        docs.insert(0, lines[j].replaceFirst(_tripleSlash, '').trim());
        j--;
      } else if (t.startsWith('@')) {
        // Annotation between the doc block and the declaration.
        j--;
      } else {
        break;
      }
    }
    if (docs.isEmpty) return null;
    return docs.first.isEmpty ? null : docs.first;
  }

  /// Masked body text of [method] (comments/strings blanked).
  String maskedBody(ScannedMethod method) =>
      masked.substring(method.bodyStart, method.bodyEnd);
}

// ---------------------------------------------------------------------------
// Mask phase
// ---------------------------------------------------------------------------

class _CodeFrame {
  _CodeFrame({required this.interpolation});

  final bool interpolation;
  int braceDepth = 0;
}

class _StringFrame {
  _StringFrame({
    required this.quote,
    required this.raw,
    required this.triple,
  });

  final int quote;
  final bool raw;
  final bool triple;
}

bool _isIdentPart(String ch) =>
    ch == '_' ||
    ch == r'$' ||
    (ch.codeUnitAt(0) >= 0x30 && ch.codeUnitAt(0) <= 0x39) ||
    (ch.codeUnitAt(0) >= 0x41 && ch.codeUnitAt(0) <= 0x5A) ||
    (ch.codeUnitAt(0) >= 0x61 && ch.codeUnitAt(0) <= 0x7A);

class _MaskOutput {
  const _MaskOutput(this.text, this.comments);

  final String text;
  final List<CommentSpan> comments;
}

_MaskOutput _maskSource(String src) {
  final out = StringBuffer();
  final comments = <CommentSpan>[];
  final frames = <Object>[_CodeFrame(interpolation: false)];
  var i = 0;

  void writeMasked(int from, int to) {
    for (var k = from; k < to; k++) {
      out.write(src[k] == '\n' ? '\n' : ' ');
    }
  }

  while (i < src.length) {
    final frame = frames.last;
    if (frame is _CodeFrame) {
      final ch = src[i];
      final next = i + 1 < src.length ? src[i + 1] : '';
      if (ch == '/' && next == '/') {
        var j = i;
        while (j < src.length && src[j] != '\n') {
          j++;
        }
        comments.add(CommentSpan(i, j, src.substring(i, j)));
        writeMasked(i, j);
        i = j;
        continue;
      }
      if (ch == '/' && next == '*') {
        final start = i;
        var depth = 0;
        var j = i;
        while (j < src.length) {
          if (j + 1 < src.length && src[j] == '/' && src[j + 1] == '*') {
            depth++;
            j += 2;
          } else if (j + 1 < src.length && src[j] == '*' && src[j + 1] == '/') {
            depth--;
            j += 2;
            if (depth == 0) break;
          } else {
            j++;
          }
        }
        comments.add(CommentSpan(start, j, src.substring(start, j)));
        writeMasked(start, j);
        i = j;
        continue;
      }
      if (ch == '{') {
        frame.braceDepth++;
        out.write(ch);
        i++;
        continue;
      }
      if (ch == '}') {
        if (frame.interpolation && frame.braceDepth == 0) {
          frames.removeLast();
        } else {
          frame.braceDepth--;
        }
        out.write(ch);
        i++;
        continue;
      }
      if (ch == "'" || ch == '"') {
        var isRaw = false;
        if (i > 0 && src[i - 1] == 'r') {
          final before = i >= 2 ? src[i - 2] : ' ';
          if (!_isIdentPart(before)) isRaw = true;
        }
        final triple =
            i + 2 < src.length && src[i + 1] == ch && src[i + 2] == ch;
        frames.add(_StringFrame(
          quote: ch.codeUnitAt(0),
          raw: isRaw,
          triple: triple,
        ));
        if (triple) {
          writeMasked(i, i + 3);
          i += 3;
        } else {
          out.write(' ');
          i++;
        }
        continue;
      }
      out.write(ch);
      i++;
      continue;
    }

    final sf = frame as _StringFrame;
    final ch = src[i];
    if (!sf.raw && ch == r'\') {
      final nxt = i + 1 < src.length ? src[i + 1] : '';
      out.write(nxt == '\n' ? '\n' : ' ');
      out.write(nxt == '\n' ? '\n' : ' ');
      i += 2;
      continue;
    }
    if (!sf.raw &&
        ch == r'$' &&
        i + 1 < src.length &&
        src[i + 1] == '{') {
      out.write('  ');
      frames.add(_CodeFrame(interpolation: true));
      i += 2;
      continue;
    }
    final q = String.fromCharCode(sf.quote);
    if (sf.triple) {
      if (ch == q &&
          i + 2 < src.length &&
          src[i + 1] == q &&
          src[i + 2] == q) {
        writeMasked(i, i + 3);
        frames.removeLast();
        i += 3;
        continue;
      }
    } else if (ch == q) {
      out.write(' ');
      frames.removeLast();
      i++;
      continue;
    }
    out.write(ch == '\n' ? '\n' : ' ');
    i++;
  }
  return _MaskOutput(out.toString(), comments);
}

// ---------------------------------------------------------------------------
// Structure phase
// ---------------------------------------------------------------------------

final _classLikeRe = RegExp(
  r'^[ \t]*(?:(?:abstract|base|interface|final|sealed)\s+)*(?:mixin\s+)?'
  r'(class|mixin|enum|extension)\s+([A-Za-z_$][\w$]*)',
  multiLine: true,
);

/// Group 1 is the full declaration prefix (annotations, modifiers, optional
/// return type); group 2 is the declared name immediately followed by the
/// parameter list. Group offsets are recovered from `group(1).length`.
final _callableRe = RegExp(
  r'^[ \t]*'
  r'((?:@\w+(?:\([^)]*\))?\s+)*'
  r'(?:(?:static|const|final|late|external|covariant|factory|abstract)\s+)*'
  r'(?:[A-Za-z_$][\w$]*\s*(?:<[^=;={}()]*>)?\??\s+)?)'
  r'([A-Za-z_$][\w$]*)\s*'
  r'(?:<[^=;={}]*>)?'
  r'\(',
);

const _topLevelKeywordBlocklist = {
  'import',
  'export',
  'part',
  'library',
  'typedef',
  'class',
  'mixin',
  'enum',
  'extension',
};

int? _matchBrace(String masked, int openOffset) {
  var depth = 0;
  for (var j = openOffset; j < masked.length; j++) {
    final c = masked[j];
    if (c == '{') {
      depth++;
    } else if (c == '}') {
      depth--;
      if (depth == 0) return j;
    }
  }
  return null;
}

int? _matchParen(String masked, int openOffset) {
  var depth = 0;
  for (var j = openOffset; j < masked.length; j++) {
    final c = masked[j];
    if (c == '(') {
      depth++;
    } else if (c == ')') {
      depth--;
      if (depth == 0) return j;
    }
  }
  return null;
}

/// Resolves the body range of a callable whose parameter list closes at
/// [closeParen]. Handles `async`/`sync*` modifiers and `:` initializer lists.
({int start, int end})? _resolveBody(String masked, int closeParen) {
  var i = closeParen + 1;
  while (i < masked.length) {
    final ch = masked[i];
    if (ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r') {
      i++;
      continue;
    }
    if (ch == ':') {
      // Constructor initializer list - keep scanning for the real body.
      i++;
      continue;
    }
    if (ch == '{') {
      final end = _matchBrace(masked, i);
      if (end == null) return null;
      return (start: i + 1, end: end);
    }
    if (ch == '=' && i + 1 < masked.length && masked[i + 1] == '>') {
      final semi = masked.indexOf(';', i + 2);
      if (semi < 0) return null;
      return (start: i + 2, end: semi);
    }
    if (_isIdentPart(ch)) {
      // `async`, `sync*`, `async*` - consume the word and any stars/spaces.
      var j = i;
      while (j < masked.length && _isIdentPart(masked[j])) {
        j++;
      }
      while (j < masked.length &&
          (masked[j] == '*' || masked[j] == ' ' || masked[j] == '\t')) {
        j++;
      }
      // Only genuinely accepted if a body follows; otherwise bail out to the
      // next char to avoid infinite loops on unexpected tokens.
      if (j == i) {
        return null;
      }
      i = j;
      continue;
    }
    // `;` (abstract/external) or anything unexpected: not a callable body.
    return null;
  }
  return null;
}

/// Line index of [offset] given precomputed line-start offsets.
int _lineIndexAt(List<int> lineStarts, int offset) {
  var lo = 0;
  var hi = lineStarts.length - 1;
  while (lo < hi) {
    final mid = (lo + hi + 1) >> 1;
    if (lineStarts[mid] <= offset) {
      lo = mid;
    } else {
      hi = mid - 1;
    }
  }
  return lo;
}

List<int> _computeLineStarts(String text) {
  final starts = <int>[0];
  for (var i = 0; i < text.length; i++) {
    if (text.codeUnitAt(i) == 0x0A) starts.add(i + 1);
  }
  return starts;
}

/// Segment of line [line] clipped to [from, to) as a string.
String _lineSegment(String masked, List<int> lineStarts, int line, int from, int to) {
  final ls = lineStarts[line];
  var le = line + 1 < lineStarts.length ? lineStarts[line + 1] : masked.length;
  le -= 1; // drop trailing newline if present
  if (le > to) le = to;
  final start = ls < from ? from : ls;
  if (start >= le) return '';
  return masked.substring(start, le);
}

/// Counts braces in [segment].
(int, int) _countBraces(String segment) {
  var open = 0;
  var close = 0;
  for (var i = 0; i < segment.length; i++) {
    if (segment[i] == '{') {
      open++;
    } else if (segment[i] == '}') {
      close++;
    }
  }
  return (open, close);
}

/// Scans [source] and returns its deterministic structural summary.
ScanResult scanSource(String source) {
  final maskOut = _maskSource(source);
  final masked = maskOut.text;
  final lineStarts = _computeLineStarts(source);
  final lines = source.split('\n');

  // --- class-like declarations ---
  final classes = <ScannedClass>[];
  for (final m in _classLikeRe.allMatches(masked)) {
    final name = m.group(2)!;
    final declLine = _lineIndexAt(lineStarts, m.start);
    var brace = -1;
    for (var j = m.end; j < masked.length; j++) {
      if (masked[j] == '{') {
        brace = j;
        break;
      }
      if (masked[j] == ';') break; // malformed / forward declaration
    }
    if (brace < 0) continue;
    final end = _matchBrace(masked, brace);
    if (end == null) continue;
    classes.add(ScannedClass(
      name: name,
      keyword: m.group(1)!,
      declLine: declLine,
      bodyStart: brace,
      bodyEnd: end + 1,
      methods: [],
    ));
  }
  classes.sort((a, b) => a.bodyStart.compareTo(b.bodyStart));

  // --- members of each class-like body ---
  ScannedMethod? tryCallable(int line, String segment) {
    final m = _callableRe.firstMatch(segment);
    if (m == null) return null;
    final name = m.group(2)!;
    if (name == 'Function' || name == 'set' || name == 'get') return null;
    final segStartAbs = lineStarts[line];
    final parenOpen = segStartAbs + m.end - 1; // offset of '('
    final closeParen = _matchParen(masked, parenOpen);
    if (closeParen == null) return null;
    final body = _resolveBody(masked, closeParen);
    if (body == null) return null;
    // Name starts right after the prefix group within this segment.
    final nameOffset = segStartAbs + m.start + m.group(1)!.length;
    final nameLine = _lineIndexAt(lineStarts, nameOffset);
    return ScannedMethod(
      name: name,
      nameLine: nameLine,
      bodyStart: body.start,
      bodyEnd: body.end,
    );
  }

  for (final cls in classes) {
    var depth = 0;
    final firstLine = _lineIndexAt(lineStarts, cls.bodyStart + 1);
    final lastLine = _lineIndexAt(lineStarts, cls.bodyEnd);
    for (var ln = firstLine; ln <= lastLine; ln++) {
      final seg = _lineSegment(masked, lineStarts, ln, cls.bodyStart + 1, cls.bodyEnd);
      if (seg.isEmpty) continue;
      if (depth == 0) {
        final found = tryCallable(ln, seg);
        if (found != null) {
          cls.methods.add(found);
        }
      }
      final (open, close) = _countBraces(seg);
      depth += open - close;
      if (depth < 0) depth = 0;
    }
  }

  // --- top-level functions (depth 0, outside every class body) ---
  final topLevelFunctions = <ScannedMethod>[];
  var classPtr = 0;
  var depth = 0;
  final totalLines = lineStarts.length;
  for (var ln = 0; ln < totalLines; ln++) {
    final lineStart = lineStarts[ln];
    while (classPtr < classes.length && classes[classPtr].bodyEnd <= lineStart) {
      classPtr++;
    }
    final inClass =
        classPtr < classes.length && classes[classPtr].bodyStart <= lineStart;
    final seg = inClass
        ? ''
        : _lineSegment(masked, lineStarts, ln, 0, masked.length);
    if (!inClass && depth == 0 && seg.isNotEmpty) {
      final trimmed = seg.trimLeft();
      final firstWord =
          RegExp(r'^[A-Za-z_]\w*').firstMatch(trimmed)?.group(0) ?? '';
      if (!_topLevelKeywordBlocklist.contains(firstWord)) {
        final found = tryCallable(ln, seg);
        if (found != null) topLevelFunctions.add(found);
      }
    }
    final (open, close) = _countBraces(inClass ? '' : seg);
    depth += open - close;
    if (depth < 0) depth = 0;
  }

  return ScanResult(
    source: source,
    masked: masked,
    lines: lines,
    classes: classes,
    topLevelFunctions: topLevelFunctions,
    docComments: maskOut.comments.where((c) => c.text.trimLeft().startsWith('///')).toList(),
  );
}
