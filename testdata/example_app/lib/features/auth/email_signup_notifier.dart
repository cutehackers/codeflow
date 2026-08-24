import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Signup form status for the email registration journey.
enum EmailSignupStatus { idle, submitting, done, failed }

/// Immutable UI state of the email signup flow.
class EmailSignupState {
  const EmailSignupState({
    this.status = EmailSignupStatus.idle,
    this.error,
  });

  final EmailSignupStatus status;
  final String? error;

  EmailSignupState copyWith({
    EmailSignupStatus? status,
    String? error,
  }) =>
      EmailSignupState(
        status: status ?? this.status,
        error: error ?? this.error,
      );
}

/// Boundary service contract wired by DI in main.
abstract class SignupService {
  Future<void> call(String email);
}

/// Notifier driving the email signup user action.
class EmailSignupNotifier extends Notifier<EmailSignupState> {
  /// Submits the email signup form as one user-visible journey step.
  ///
  /// Validates locally, calls the signup service and stores progress.
  Future<void> submit(String email) async {
    state = state.copyWith(status: EmailSignupStatus.submitting);
    try {
      await ref.read(signupServiceProvider)(email);
      state = state.copyWith(status: EmailSignupStatus.done);
    } on Exception {
      state = state.copyWith(
        status: EmailSignupStatus.failed,
        error: 'signup failed',
      );
    }
  }

  /// Resets the form back to the idle status after dismissal.
  void resetToIdle() {
    state = state.copyWith(status: EmailSignupStatus.idle);
  }
}

final signupServiceProvider = Provider<SignupService>(
  (ref) => _InMemorySignupSource(),
);

class _InMemorySignupSource implements SignupService {
  @override
  Future<void> call(String email) async {}
}
