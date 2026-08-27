// Eval fixture: second entry point. "Sheet" is not in any keyword list;
// the entry-seed rule must place it.
class AdminSheet {
  final Dispatcher _dispatcher = Dispatcher();

  void show() {
    _dispatcher.run('admin-open');
  }
}
