import 'dart:convert';
import 'dart:io';

import 'package:codeflow_dart_adapter/codeflow_dart_adapter.dart';

Future<void> main(List<String> args) async {
  final root = args.first;
  final result = harvestCandidates({
    'repoRoot': root,
    'profiles': [
      {
        'name': 'nonsense-framework',
        'patterns': {'domainMarkers': [r'Nope$']},
      },
    ],
  });
  for (final c in result['candidates'] as List) {
    stdout.writeln(jsonEncode(c));
  }
  stderr.writeln('detect: ${jsonEncode(detectRepo(repoRoot: root))}');
}
