async function retryRequest() { return true; }
export async function retryUntilDone(done: boolean) {
  while (!done) { await retryRequest(); }
  finish();
}
function finish() {}
