// codeflow_dart_adapter - CodeFlow v2 Dart language adapter.
//
// Speaks the CORE adapter protocol (NDJSON over stdio). Ops: ping, detect,
// harvest_candidates, slice (E_BAD_REQUEST until ticket 07), shutdown.
library;

import 'dart:convert';
import 'dart:io';

import 'package:codeflow_dart_adapter/src/protocol.dart';

Future<void> main() async {
  final server = AdapterServer(
    requests: stdin
        .transform(utf8.decoder)
        .transform(const LineSplitter()),
    respond: stdout.writeln,
  );
  await server.serve();
  await stdout.flush();
  exit(0);
}
