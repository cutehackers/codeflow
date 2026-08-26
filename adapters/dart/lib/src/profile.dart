// Framework profile loading (design §5.3).
//
// Profiles absorb framework differences declaratively. Built-in sets
// (riverpod-like, bloc, go_router) are encoded as Dart maps - the adapter is
// stdlib-only, so the YAML text form stays a CORE-side concern; the wire
// format carries parsed pattern arrays:
//
//   params.profiles: [
//     {"name": "riverpod",
//      "patterns": {
//        "domainMarkers":    ["[regex strings]"],
//        "stateMutations":   ["[regex strings]"],
//        "boundarySuffixes": ["Repository", ...]}}
//   ]
//
// Merge semantics: built-ins always load. A provided profile whose name
// matches a known built-in EXTENDS that built-in's pattern lists (union,
// append order = params order). Unknown names are ignored with a stderr
// warning (stderr is safe; stdout is protocol-only).

/// Pattern bundle of one framework set.
class ProfilePatterns {
  const ProfilePatterns({
    this.domainMarkers = const [],
    this.stateMutations = const [],
    this.boundarySuffixes = const [],
  });

  /// Class-qualified regexes identifying marker-bearing symbols, e.g.
  /// `*Notifier.{method}` from riverpod.yaml becomes `[A-Za-z0-9_]+Notifier$`.
  final List<String> domainMarkers;

  /// Regexes over method bodies that count as state mutation evidence
  /// (`state =` assignment, `copyWith(` call, `emit(` call, ...).
  final List<String> stateMutations;

  /// Class-name suffixes marking external boundaries (design §4.2 Stage 2).
  final List<String> boundarySuffixes;
}

/// Effective merged profile used by the harvester.
class FrameworkProfile {
  FrameworkProfile({
    required this.sourceNames,
    required ProfilePatterns patterns,
  })  : domainMarkerRegexes =
            patterns.domainMarkers.map(_compile).whereType<RegExp>().toList(),
        stateMutationRegexes =
            patterns.stateMutations.map(_compile).whereType<RegExp>().toList(),
        boundarySuffixes = List<String>.unmodifiable(patterns.boundarySuffixes);

  final List<String> sourceNames;
  final List<RegExp> domainMarkerRegexes;
  final List<RegExp> stateMutationRegexes;
  final List<String> boundarySuffixes;

  static RegExp? _compile(String pattern) {
    try {
      return RegExp(pattern);
    } on FormatException {
      return null;
    }
  }

  bool matchesBoundaryClass(String className) => boundarySuffixes.any(
        (suffix) =>
            suffix.isNotEmpty &&
            className.endsWith(suffix) &&
            className != suffix,
      );
}

const Map<String, ProfilePatterns> _builtinProfiles = {
  // Mirrors profiles/riverpod.yaml.
  'riverpod': ProfilePatterns(
    domainMarkers: [r'[A-Za-z0-9_]+Notifier$', r'[A-Za-z0-9_]+Controller$'],
    stateMutations: [r'\bstate\s*=[^=]', r'\.copyWith\s*\('],
    boundarySuffixes: ['Repository', 'ApiClient'],
  ),
  // Mirrors profiles/bloc.yaml.
  'bloc': ProfilePatterns(
    domainMarkers: [r'[A-Za-z0-9_]+(Bloc|Cubit)$'],
    stateMutations: [r'\.copyWith\s*\(', r'\bemit\s*\('],
    boundarySuffixes: ['Repository', 'ApiClient'],
  ),
  // Mirrors profiles/go_router.yaml: route callbacks are detected
  // structurally (navigation calls / deep-link params), so no class-suffix
  // domain markers are needed here.
  'go_router': ProfilePatterns(
    domainMarkers: [],
    stateMutations: [],
    boundarySuffixes: ['Repository', 'ApiClient'],
  ),
};

/// Names recognized during [resolveProfiles]; anything else warns+ignores.
Iterable<String> get knownProfileNames => _builtinProfiles.keys;

/// The default effective profile: union of all built-in sets.
FrameworkProfile defaultProfile() {
  var domainMarkers = <String>[];
  var stateMutations = <String>[];
  var boundarySuffixes = <String>[];
  for (final p in _builtinProfiles.values) {
    domainMarkers = [...domainMarkers, ...p.domainMarkers];
    stateMutations = [...stateMutations, ...p.stateMutations];
    boundarySuffixes = [...boundarySuffixes, ...p.boundarySuffixes];
  }
  return FrameworkProfile(
    sourceNames: _builtinProfiles.keys.toList(growable: false),
    patterns: ProfilePatterns(
      domainMarkers: domainMarkers,
      stateMutations: stateMutations,
      boundarySuffixes: boundarySuffixes,
    ),
  );
}

/// Merges wire-format [specs] (decoded `params.profiles`) into an effective
/// profile. Unknown names are reported on stderr and skipped; malformed
/// entries are warned and skipped as well.
FrameworkProfile resolveProfiles(Object? specs) {
  final base = defaultProfile();
  if (specs == null) return base;

  var domainMarkers = <String>[];
  var stateMutations = <String>[];
  var boundarySuffixes = <String>[];
  for (final p in _builtinProfiles.values) {
    domainMarkers = [...domainMarkers, ...p.domainMarkers];
    stateMutations = [...stateMutations, ...p.stateMutations];
    boundarySuffixes = [...boundarySuffixes, ...p.boundarySuffixes];
  }

  final names = <String>[];

  if (specs is List) {
    for (final entry in specs) {
      if (entry is! Map) {
        _warn('ignoring malformed profile entry (expected object): $entry');
        continue;
      }
      final name = entry['name'];
      if (name is! String || !_builtinProfiles.containsKey(name)) {
        _warn('ignoring unknown framework profile: $name '
            '(known: ${knownProfileNames.join(", ")})');
        continue;
      }
      names.add(name);
      final rawPatterns = entry['patterns'];
      if (rawPatterns == null) continue;
      if (rawPatterns is! Map) {
        _warn('ignoring malformed patterns for profile "$name"');
        continue;
      }
      final add = _decodePatterns(rawPatterns);
      domainMarkers = [...domainMarkers, ...add.domainMarkers];
      stateMutations = [...stateMutations, ...add.stateMutations];
      boundarySuffixes = [...boundarySuffixes, ...add.boundarySuffixes];
    }
  } else {
    _warn('params.profiles must be an array; ignoring: $specs');
  }

  return FrameworkProfile(
    sourceNames: [
      ...base.sourceNames,
      ...names,
    ],
    patterns: ProfilePatterns(
      domainMarkers: domainMarkers.toSet().toList(),
      stateMutations: stateMutations.toSet().toList(),
      boundarySuffixes: boundarySuffixes.toSet().toList(),
    ),
  );
}

ProfilePatterns _decodePatterns(Map raw) {
  List<String> strings(Object? key) {
    final v = raw[key];
    if (v is! List) return const [];
    return v.whereType<String>().toList();
  }

  return ProfilePatterns(
    domainMarkers: strings('domainMarkers'),
    stateMutations: strings('stateMutations'),
    boundarySuffixes: strings('boundarySuffixes'),
  );
}

void _warn(String message) {
  // stderr is safe: the CORE protocol owns stdout exclusively.
  // ignore: avoid_print
  print(message);
}
