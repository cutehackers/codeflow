import 'package:flutter/foundation.dart';

/// Persisted display settings of the shop shell.
class AppSettings {
  const AppSettings({this.darkMode = false});

  final bool darkMode;

  AppSettings copyWith({bool? darkMode}) =>
      AppSettings(darkMode: darkMode ?? this.darkMode);
}

/// Legacy ChangeNotifier-style holder kept beside Riverpod during migration.
class SettingsNotifier extends ChangeNotifier {
  SettingsNotifier() : state = const AppSettings();

  AppSettings state;

  /// Standalone state transition: flips dark mode without a business journey.
  void toggleDarkMode(bool enabled) {
    state = state.copyWith(darkMode: enabled);
    notifyListeners();
  }
}
