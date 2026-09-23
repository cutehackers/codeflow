type RouteRequest = { headers: { get(name: string): string | null } };

export async function POST(request: RouteRequest) {
  const credential = request.headers.get('authorization');
  if (!credential) return Response.json({ error: 'unauthorized' }, { status: 401 });
  return createOrder(credential);
}

function createOrder(credential: string) {
  return { accepted: true, credential };
}
