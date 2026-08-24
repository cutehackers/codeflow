import 'dart:convert';

import 'package:test/test.dart';

import 'package:codeflow_dart_adapter/src/harvest.dart';
import 'package:codeflow_dart_adapter/src/sha256.dart';
import 'helpers.dart';

void main() {
  test('identical input tree yields byte-identical results across runs', () {
    final params = {'repoRoot': exampleAppRoot()};
    final run1 = harvestCandidates(params);
    final run2 = harvestCandidates(params);
    expect(jsonEncode(run1), jsonEncode(run2));

    // Line-level equality too (NDJSON framing is what CORE consumes).
    final lines1 = (run1['candidates']! as List<dynamic>)
        .map((c) => jsonEncode(c))
        .toList();
    final lines2 = (run2['candidates']! as List<dynamic>)
        .map((c) => jsonEncode(c))
        .toList();
    expect(lines1, lines2);
  });

  test('candidates are strictly sorted by entrySymbolPath', () {
    final result = harvestCandidates({'repoRoot': exampleAppRoot()});
    final paths = (result['candidates']! as List<dynamic>)
        .map((c) => (c! as Map)['entrySymbolPath'] as String)
        .toList();
    final sorted = [...paths]..sort();
    expect(paths, sorted);
  });

  test('candidateIds are stable and unique per symbol path', () {
    final result = harvestCandidates({
      'repoRoot': exampleAppRoot(),
      'profiles': [
        {
          'name': 'bloc',
          'patterns': {
            'stateMutations': [r'\bemit\s*\('],
          },
        },
      ],
    });
    final candidates =
        (result['candidates']! as List).cast<Map>();
    expect(candidates, isNotEmpty);
    final ids = candidates.map((c) => c['candidateId']).toSet();
    expect(ids.length, candidates.length);
    for (final c in candidates) {
      expect(
        c['candidateId'],
        candidateIdFor(c['entrySymbolPath'] as String),
      );
    }
  });

  test('missing repoRoot / absent lib dir behave deterministically', () {
    expect(
      () => harvestCandidates({}),
      throwsArgumentError,
    );

    final empty = harvestCandidates({'repoRoot': exampleAppRoot(), 'libSubdir': 'nope'});
    expect((empty['candidates']! as List).isEmpty, isTrue);
  });
}
