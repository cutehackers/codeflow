async function save() { return 1; }
async function notify() { return 2; }
export async function checkout() {
  const result = await Promise.all([save(), notify()]);
  finish(result);
}
function finish(value: unknown[]) { return value.length; }
