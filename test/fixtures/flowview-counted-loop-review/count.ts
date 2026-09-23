export function processItems(limit: number) {
  for (let index = 0; index < limit; index++) {
    apply(index);
  }
  finish();
}

function apply(index: number) {
  return index;
}

function finish() {}
