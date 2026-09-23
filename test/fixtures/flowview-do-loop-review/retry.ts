export function processUntilDone(again: boolean) {
  do {
    apply();
  } while (again);
  finish();
}

function apply() {}

function finish() {}
