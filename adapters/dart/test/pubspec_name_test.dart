import 'dart:io';

import 'package:test/test.dart';

import 'package:codeflow_dart_adapter/src/harvest.dart';
import 'package:codeflow_dart_adapter/src/sha256.dart';
import 'helpers.dart';

void main() {
  test('pubspec name is extracted from the example app fixture', () {
    expect(readPackageName(exampleAppRoot()), 'shop_app');
  });

  test('missing pubspec yields null; malformed pubspec yields null', () {
    final tmp = Directory.systemTemp.createTempSync('codeflow_pubspec_test');
    addTearDown(() => tmp.deleteSync(recursive: true));
    expect(readPackageName(tmp.path), isNull);

    File('${tmp.path}/pubspec.yaml').writeAsStringSync('not: a\nnameless one');
    expect(readPackageName(tmp.path), isNull);

    File('${tmp.path}/pubspec.yaml').writeAsStringSync('name: with_comments # trailing\n');
    expect(readPackageName(tmp.path), 'with_comments');
  });

  test('sha256 matches published FIPS 180-4 vectors', () {
    // sha256('abc')
    expect(sha256Hex('abc'),
        'ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad');
    // sha256('')
    expect(
        sha256Hex(''),
        'e3b0c44298fc1c149afbf4c8996fb924'
        '27ae41e4649b934ca495991b7852b855');
    // sha256('abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq')
    expect(
        sha256Hex(
            'abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq'),
        '248d6a61d20638b8e5c026930c3e6039a33ce45964ff2167f6ecedd419db06c1');
  });

  test('candidateId derivation is the normative cand-<16 hex> form', () {
    final id = candidateIdFor('main.dart#firebaseMessagingBackgroundHandler');
    expect(id, matches(RegExp(r'^cand-[a-z0-9]{16}$')));
    expect(id, 'cand-292e523e744f7b1a'); // cross-checked with hashlib
  });
}
