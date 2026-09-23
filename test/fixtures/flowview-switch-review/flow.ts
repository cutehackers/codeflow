export function selectAction(action: string) {
  switch (action) {
    case "approve": approve(); break;
    case "reject": reject(); break;
    default: review();
  }
  finish();
}
function approve() {}
function reject() {}
function review() {}
function finish() {}
