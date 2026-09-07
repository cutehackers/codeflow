// Shared diagnostic redaction for the Dart adapter.
//
// Redaction is applied to structured values before JSON encoding and to raw
// text before byte clipping. This keeps malformed diagnostics and locally
// constructed error details on the same single-gate path.
library;

import 'dart:convert';

const String redactionMarker = '***REDACTED***';

final RegExp _secretAssignmentPattern = RegExp(
  r'''(?:(?:\b(?:api[_-]?key|secret|token|password|credential|authorization|private[_-]?key|access[_-]?token|client[_-]?secret)\b)|[A-Za-z][A-Za-z0-9]*(?:api[_-]?key|secret|token|password|credential|authorization|private[_-]?key|access[_-]?token|client[_-]?secret))\s*[:=]\s*['"]?[^\s;'"}]+['"]?''',
  caseSensitive: false,
);

// Handles incomplete JSON where the value extends beyond the available
// diagnostic bytes, including prefixed/suffixed key names.
final RegExp _quotedSecretPattern = RegExp(
  r'''"(?:(?:api[_-]?key|secret|token|password|credential|authorization|private[_-]?key|access[_-]?token|client[_-]?secret)[A-Za-z0-9_-]*|[A-Za-z][A-Za-z0-9_-]*(?:api[_-]?key|secret|token|password|credential|authorization|private[_-]?key|access[_-]?token|client[_-]?secret)[A-Za-z0-9_-]*)"\s*:\s*(?:"[^"\\]*(?:\\.[^"\\]*)*"?|[^,\s}\]]+)''',
  caseSensitive: false,
);

class DiagnosticRedaction {
  const DiagnosticRedaction(this.value, this.count);

  final Object? value;
  final int count;
}

DiagnosticRedaction redactValue(Object? value) {
  if (value is String) {
    return _redactRaw(value);
  }
  if (value is Map) {
    var count = 0;
    final output = <String, Object?>{};
    for (final entry in value.entries) {
      final key = entry.key.toString();
      if (_sensitiveKey(key)) {
        count++;
        output[key] = redactionMarker;
        continue;
      }
      final clean = redactValue(entry.value);
      count += clean.count;
      output[key] = clean.value;
    }
    return DiagnosticRedaction(output, count);
  }
  if (value is Iterable) {
    var count = 0;
    final output = <Object?>[];
    for (final item in value) {
      final clean = redactValue(item);
      count += clean.count;
      output.add(clean.value);
    }
    return DiagnosticRedaction(output, count);
  }
  return DiagnosticRedaction(value, 0);
}

/// Redacts a diagnostic and then bounds its UTF-8 representation.
String redactDiagnostic(Object? value,
    {int maxBytes = 512, int maxItems = 64}) {
  Object? safe;
  if (value is String) {
    final trimmed = value.trimLeft();
    if (trimmed.startsWith('{') || trimmed.startsWith('[')) {
      try {
        safe = redactValue(jsonDecode(value)).value;
      } catch (_) {
        safe = _redactRaw(value).value;
      }
    } else {
      safe = _redactRaw(value).value;
    }
  } else {
    safe = redactValue(value).value;
  }

  if (safe is Iterable && maxItems > 0) {
    safe = safe.take(maxItems).toList();
  }
  String text;
  try {
    text = safe is String ? safe : jsonEncode(safe);
  } catch (_) {
    text = _redactRaw(value?.toString() ?? '').value as String;
  }
  if (maxBytes <= 0) return text;
  final bytes = utf8.encode(text);
  if (bytes.length <= maxBytes) return text;
  return utf8.decode(bytes.sublist(0, maxBytes), allowMalformed: true);
}

DiagnosticRedaction _redactRaw(String input) {
  var count = 0;
  var text = input.replaceAllMapped(_quotedSecretPattern, (_) {
    count++;
    return redactionMarker;
  });
  text = text.replaceAllMapped(_secretAssignmentPattern, (_) {
    count++;
    return redactionMarker;
  });
  return DiagnosticRedaction(text, count);
}

bool _sensitiveKey(String raw) {
  final key = raw.trim().toLowerCase();
  if (key.isEmpty ||
      !RegExp(
        r'(?:api[_-]?key|secret|token|password|credential|authorization|private[_-]?key|access[_-]?token|client[_-]?secret)',
        caseSensitive: false,
      ).hasMatch(key)) {
    return false;
  }
  final compact = key.replaceAll(RegExp(r'[-_\s]'), '');
  const markers = [
    'apikey',
    'secret',
    'token',
    'password',
    'credential',
    'authorization',
    'privatekey',
    'accesskey',
    'accesstoken',
    'clientsecret',
  ];
  return markers.any((marker) =>
      compact == marker ||
      compact.startsWith(marker) ||
      compact.endsWith(marker));
}
