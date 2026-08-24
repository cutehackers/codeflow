// Deterministic identifier-rule humanization (design §8.1, provenance
// 'derived'). The full Korean naming engine lands in ticket 11; this module
// only does the English fallback: split camelCase/snake_case words and join
// them as an English sentence fragment, e.g.
//   submitOrder          -> "Submit order"
//   _onItemAdded         -> "Item added"
//   onCheckoutPressed    -> "Checkout pressed"
//   firebaseMessagingBackgroundHandler
//                        -> "Firebase messaging background handler"
//   URLLoader            -> "Url loader"

bool _isUpperAt(String s, int i) {
  if (i < 0 || i >= s.length) return false;
  final ch = s.substring(i, i + 1);
  return ch.toUpperCase() == ch && ch.toLowerCase() != ch;
}

bool _isDigitAt(String s, int i) {
  if (i < 0 || i >= s.length) return false;
  final c = s.codeUnitAt(i);
  return c >= 0x30 && c <= 0x39;
}

/// Splits [s] on underscores, dollar signs and camelCase boundaries.
/// Consecutive uppercase letters stay together until an acronym run ends:
/// `URLLoader` -> `[URL, Loader]`.
List<String> splitIdentifierWords(String s) {
  final words = <String>[];
  final current = StringBuffer();
  void flush() {
    if (current.isNotEmpty) {
      words.add(current.toString());
      current.clear();
    }
  }

  for (var i = 0; i < s.length; i++) {
    final ch = s[i];
    if (ch == '_' || ch == r'$') {
      flush();
      continue;
    }
    if (_isUpperAt(s, i)) {
      // camel boundary: "fooBar"/"foo9Bar" -> foo | Bar
      final afterLowerOrDigit =
          current.isNotEmpty && (_isLowerAt(s, i - 1) || _isDigitAt(s, i - 1));
      // acronym-run end: "URLLoader" at 'L' -> URL | Loader
      final acronymEnd =
          current.length >= 2 && _isUpperAt(s, i - 1) && _isLowerAt(s, i + 1);
      if (afterLowerOrDigit || acronymEnd) {
        flush();
      }
    }
    current.write(ch);
  }
  flush();
  return words;
}

bool _isLowerAt(String s, int i) {
  if (i < 0 || i >= s.length) return false;
  final ch = s.substring(i, i + 1);
  return ch.toLowerCase() == ch && ch.toUpperCase() != ch;
}

/// Humanizes a Dart symbol name into a short English phrase. Always returns a
/// non-empty string (falls back to the trimmed raw name).
String humanizeIdentifier(String rawName) {
  var name = rawName.trim();
  while (name.startsWith('_')) {
    name = name.substring(1);
  }
  // Drop a leading event-handler "on" prefix when followed by an uppercase
  // letter: onItemAdded -> ItemAdded.
  if (name.length > 2 &&
      name.startsWith('on') &&
      _isAsciiUpper(name.codeUnitAt(2))) {
    name = name.substring(2);
  }
  final words = splitIdentifierWords(name)
      .map((w) => w.toLowerCase())
      .where((w) => w.isNotEmpty)
      .toList();
  if (words.isEmpty) {
    return 'unnamed';
  }
  final first = words.first;
  final buf = StringBuffer()
    ..write(first.substring(0, 1).toUpperCase())
    ..write(first.substring(1));
  for (var i = 1; i < words.length; i++) {
    buf.write(' ');
    buf.write(words[i]);
  }
  return buf.toString();
}

bool _isAsciiUpper(int codeUnit) => codeUnit >= 0x41 && codeUnit <= 0x5A;
