// Eval fixture: state holder. No state/notifier/bloc keywords; the layer
// comes from the observed mutation (stateDelta) evidence.
class Keeper {
  String _phase = 'idle';

  void watch(String event) {
    _phase = 'watching';
  }
}
