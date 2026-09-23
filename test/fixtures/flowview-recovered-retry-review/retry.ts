export async function retryUntilDone(done: boolean) {
  while (!done) {
    try {
      await retryRequest();
    } catch {
      recover();
    }
  }
  finish();
}

async function retryRequest() {
  return true;
}

function recover() {}

function finish() {}
