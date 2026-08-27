// Eval fixture: persistence writer with no naming hints at all.
// Its layer must come from graph position (called by Dispatcher, calls Vault).
class Ledger {
  final Vault _vault = Vault();

  void commit(String entry) {
    _vault.put(entry);
  }
}
