// codeflow_dart_adapter - CodeFlow Dart adapter entrypoint.
//
// Production traffic uses JSON-RPC 2.0 Content-Length frames. The legacy
// line mode is retained for direct package compatibility tests only.
import 'dart:convert';
import 'dart:io';

import 'package:codeflow_dart_adapter/src/protocol.dart';

const int _maxMessageBytes = 1 << 20;
const int _maxHeaderBytes = 8 << 10;

void main() {
  final server = AdapterServer(
    requests: const Stream<String>.empty(),
    respond: stdout.writeln,
  );
  final buffer = <int>[];
  String? mode;
  var active = 0;
  final cancelled = <String>{};
  var shuttingDown = false;

  void writeRpc(Object value) {
    final body = utf8.encode(jsonEncode(value));
    stdout.add(utf8.encode('Content-Length: ${body.length}\r\n\r\n'));
    stdout.add(body);
  }

  Map<String, Object?> rpcError(String id, String code, String message,
      {bool retryable = false}) {
    final rpcCode = code == 'E_BAD_REQUEST' || code == 'E_UNSUPPORTED_VERSION'
        ? -32602
        : -32000;
    return {
      'jsonrpc': jsonRpcVersion,
      'id': id,
      'error': {
        'code': rpcCode,
        'message': message.length > 512 ? message.substring(0, 512) : message,
        'data': {'code': code, 'retryable': retryable},
      },
    };
  }

  void processLegacy() {
    while (true) {
      final newline = buffer.indexOf(10);
      if (newline < 0) return;
      final line =
          utf8.decode(buffer.sublist(0, newline), allowMalformed: true);
      buffer.removeRange(0, newline + 1);
      final response = server.handleLine(line);
      if (response != null) stdout.writeln(response);
      if (line.contains('"op":"shutdown"')) shuttingDown = true;
    }
  }

  void processFramed() {
    while (true) {
      final headerEnd = _findBytes(buffer, const [13, 10, 13, 10]);
      if (headerEnd < 0) {
        if (buffer.length > _maxHeaderBytes) {
          buffer.clear();
          writeRpc(rpcError('', 'E_BAD_REQUEST', 'frame header exceeds bound'));
        }
        return;
      }
      if (headerEnd > _maxHeaderBytes) {
        buffer.removeRange(0, headerEnd + 4);
        writeRpc(rpcError('', 'E_BAD_REQUEST', 'frame header exceeds bound'));
        continue;
      }
      final header = ascii.decode(buffer.sublist(0, headerEnd));
      final match = RegExp(r'(?:^|\r\n)Content-Length\s*:\s*(\d+)\s*(?:\r\n|$)',
              caseSensitive: false)
          .firstMatch(header);
      if (match == null) {
        buffer.removeRange(0, headerEnd + 4);
        writeRpc(
            rpcError('', 'E_BAD_REQUEST', 'frame is missing Content-Length'));
        continue;
      }
      final length = int.tryParse(match.group(1)!);
      if (length == null || length < 0) {
        buffer.removeRange(0, headerEnd + 4);
        writeRpc(rpcError('', 'E_BAD_REQUEST', 'invalid Content-Length'));
        continue;
      }
      final frameEnd = headerEnd + 4 + length;
      if (length > _maxMessageBytes) {
        if (buffer.length < frameEnd) return;
        buffer.removeRange(0, frameEnd);
        writeRpc(
            rpcError('', 'E_BAD_REQUEST', 'message exceeds maxMessageBytes'));
        continue;
      }
      if (buffer.length < frameEnd) return;
      final body = utf8.decode(buffer.sublist(headerEnd + 4, frameEnd),
          allowMalformed: true);
      buffer.removeRange(0, frameEnd);
      Object? decoded;
      try {
        decoded = jsonDecode(body);
      } catch (e) {
        writeRpc(
            rpcError('', 'E_BAD_REQUEST', 'request body is not valid JSON'));
        continue;
      }
      if (decoded is Map && decoded['method'] == r'$/cancelRequest') {
        final params = decoded['params'];
        if (params is Map && params['id'] is String) {
          cancelled.add(params['id'] as String);
        }
        continue;
      }
      if (active >= (capabilities['maxInFlight'] as int)) {
        final id = decoded is Map && decoded['id'] is String
            ? decoded['id'] as String
            : '';
        writeRpc(rpcError(
            id, 'E_BACKPRESSURE', 'adapter in-flight bound exceeded',
            retryable: true));
        continue;
      }
      active++;
      try {
        final id = decoded is Map && decoded['id'] is String
            ? decoded['id'] as String
            : '';
        if (cancelled.contains(id)) {
          cancelled.remove(id);
          writeRpc(rpcError(id, 'E_CANCELLED', 'request cancelled'));
        } else {
          final response = server.handleRpcRequest(decoded);
          if (decoded is Map &&
              decoded['method'] != 'initialize' &&
              decoded['method'] != 'ping' &&
              response['result'] != null) {
            writeRpc({
              'jsonrpc': jsonRpcVersion,
              'method': r'$/progress',
              'params': {'id': id, 'stage': 'complete'},
            });
          }
          final params = decoded is Map ? decoded['params'] : null;
          if (params is Map && params['batchId'] is String) {
            writeRpc({
              'jsonrpc': jsonRpcVersion,
              'method': 'codeflow/batchAck',
              'params': {'batchId': params['batchId'], 'acknowledged': true},
            });
          }
          writeRpc(response);
          if (decoded is Map && decoded['method'] == 'shutdown') {
            shuttingDown = true;
          }
        }
      } finally {
        active--;
      }
    }
  }

  stdin.listen((chunk) {
    buffer.addAll(chunk);
    if (mode == null) {
      final prefix =
          utf8.decode(buffer.take(32).toList(), allowMalformed: true);
      if (RegExp(r'^\s*Content-Length\s*:', caseSensitive: false)
          .hasMatch(prefix)) {
        mode = 'framed';
      } else if (buffer.contains(10)) {
        mode = 'legacy';
      }
    }
    if (mode == 'framed') processFramed();
    if (mode == 'legacy') processLegacy();
  }, onDone: () async {
    if (mode == 'legacy') processLegacy();
    if (mode == 'framed' && buffer.isNotEmpty && !shuttingDown) {
      writeRpc(
          rpcError('', 'E_BAD_REQUEST', 'incomplete Content-Length frame'));
    }
    await stdout.flush();
    exit(0);
  });
}

int _findBytes(List<int> source, List<int> needle) {
  if (needle.isEmpty) return 0;
  for (var i = 0; i + needle.length <= source.length; i++) {
    var matches = true;
    for (var j = 0; j < needle.length; j++) {
      if (source[i + j] != needle[j]) {
        matches = false;
        break;
      }
    }
    if (matches) return i;
  }
  return -1;
}
