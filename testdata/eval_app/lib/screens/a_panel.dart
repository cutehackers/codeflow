// Eval fixture: user-facing panel. Deliberately nonstandard naming —
// no page/screen/widget/dialog keywords anywhere.
class Panel {
  final Dispatcher _dispatcher = Dispatcher();

  void show() {
    _dispatcher.run('panel-open');
  }
}
