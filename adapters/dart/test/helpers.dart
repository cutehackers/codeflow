import 'dart:io';

/// Locates the adapters/dart package root by walking up from the current
/// directory until the pubspec declares this package.
String findPackageRoot() {
  var dir = Directory.current.path;
  for (var i = 0; i < 8; i++) {
    final pubspec = File('$dir/pubspec.yaml');
    if (pubspec.existsSync() &&
        pubspec.readAsStringSync().contains('name: codeflow_dart_adapter')) {
      return dir;
    }
    final parent = Directory(dir).parent.path;
    if (parent == dir) break;
    dir = parent;
  }
  throw StateError('package root not found from ${Directory.current.path}');
}

/// Locates the shared end-to-end fixture (testdata/example_app).
String exampleAppRoot() {
  var dir = findPackageRoot();
  for (var i = 0; i < 6; i++) {
    final candidate = '$dir/testdata/example_app';
    if (Directory(candidate).existsSync()) return candidate;
    final parent = Directory(dir).parent.path;
    if (parent == dir) break;
    dir = parent;
  }
  throw StateError('testdata/example_app not found');
}
