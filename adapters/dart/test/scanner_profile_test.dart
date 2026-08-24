import 'package:test/test.dart';

import 'package:codeflow_dart_adapter/src/humanize.dart';
import 'package:codeflow_dart_adapter/src/profile.dart';
import 'package:codeflow_dart_adapter/src/scanner.dart';

void main() {
  test('humanize: deterministic identifier-rule transforms (ticket 11 will '
      'replace with the Korean naming engine)', () {
    expect(humanizeIdentifier('submitOrder'), 'Submit order');
    expect(humanizeIdentifier('_onItemAdded'), 'Item added');
    expect(humanizeIdentifier('onCheckoutPressed'), 'Checkout pressed');
    expect(
      humanizeIdentifier('firebaseMessagingBackgroundHandler'),
      'Firebase messaging background handler',
    );
    expect(
      humanizeIdentifier('didChangeAppLifecycleState'),
      'Did change app lifecycle state',
    );
    expect(humanizeIdentifier('toggleDarkMode'), 'Toggle dark mode');
    expect(humanizeIdentifier('_privateThing'), 'Private thing');
    expect(humanizeIdentifier('URLLoader'), 'Url loader');
    // Always non-empty.
    expect(humanizeIdentifier('__'), 'unnamed');
  });

  test('profile resolution: built-ins merge, unknown names ignored', () {
    final defaults = defaultProfile();
    expect(defaults.stateMutationRegexes, isNotEmpty);
    expect(defaults.boundarySuffixes, containsAll(['Repository', 'ApiClient']));

    final merged = resolveProfiles([
      {
        'name': 'riverpod',
        'patterns': {
          'domainMarkers': [r'CustomStore$'],
        },
      },
    ]);
    final patterns =
        merged.domainMarkerRegexes.map((r) => r.pattern).toSet();
    expect(patterns, contains(r'CustomStore$'));
    // Built-in riverpod marker still present.
    expect(patterns, contains(RegExp(r'[A-Za-z0-9_]+Notifier$').pattern));

    final unknown = resolveProfiles([
      {'name': 'not-a-real-profile'},
    ]);
    expect(unknown.sourceNames, defaultProfile().sourceNames);
  });

  test('scanner finds classes, methods and top-level functions', () {
    const src = '''
import 'x.dart';

/// Doc for the helper.
Future<void> backgroundHelper(int code) async {
  print("string with class Fake { must not confuse } the scanner");
}

class Widget {
  Future<void> doThing(String a) async {
    if (a.isEmpty) {
      return;
    }
  }

  int get value => 1;

  static const int limit = 3;

  // A plain comment mentioning class Decoy { should be masked.
  void _hidden() => print('hi');
}

abstract class Repo {
  void attach();
}
''';
    final r = scanSource(src);

    expect(r.classes.map((c) => c.name).toList(), ['Widget', 'Repo']);
    expect(
      r.classes[0].methods.map((m) => m.name).toList(),
      ['doThing', '_hidden'],
    );
    expect(r.topLevelFunctions.single.name, 'backgroundHelper');

    final doc = r.firstDocLineAbove(r.topLevelFunctions.single.nameLine);
    expect(doc, 'Doc for the helper.');
  });
}
