import { NextResponse } from 'next/server';
import { api } from '../../../lib/api';

export async function GET(request: Request) {
  const { searchParams } = new URL(request.url);
  const token = searchParams.get('token');
  if (!token) {
    return NextResponse.json({ error: 'Missing token' }, { status: 400 });
  }
  return NextResponse.json({ authenticated: true });
}

export async function POST(request: Request) {
  const body = await request.json();
  const authResult = await api.v1.auth.login(body.username, body.password);
  return NextResponse.json(authResult);
}
