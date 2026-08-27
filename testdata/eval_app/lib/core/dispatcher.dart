// Eval fixture: orchestration node between UI and persistence/network.
// No controller/notifier/provider/bloc/cubit/usecase/service keywords.
class Dispatcher {
  final Ledger _ledger = Ledger();
  final Gateway _gateway = Gateway();

  void run(String command) {
    _ledger.commit(command);
    _gateway.send(command);
  }
}
