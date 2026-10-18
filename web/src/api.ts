export interface User {
  id: string;
  email: string;
  created_at: string;
}

export interface LoginResponse {
  token: string;
  user: User;
}

export interface RegisterResponse {
  id: string;
  email: string;
}

export interface DocMeta {
  id: string;
  name: string;
  owner_id: string;
  type: string;
  created_at: string;
}

export function getBaseUrl(): string {
  if (typeof window !== 'undefined') {
    const port = localStorage.getItem('vss_server_port');
    if (port && port !== 'undefined') {
      return `http://localhost:${port}`;
    }
  }
  return '';
}

async function handleResponse<T>(res: Response): Promise<T> {
  if (!res.ok) {
    if (res.status === 401) {
      if (typeof window !== 'undefined') {
        localStorage.removeItem('vss_token');
        localStorage.removeItem('vss_user');
        window.dispatchEvent(new CustomEvent('vss:unauthorized'));
      }
    }
    let errMsg = res.statusText;
    try {
      const data = await res.json();
      if (data && typeof data.error === 'string') {
        errMsg = data.error;
      }
    } catch {
      // ignore
    }
    throw new TypeError(errMsg);
  }
  return res.json() as Promise<T>;
}

export async function register(email: string, password: string): Promise<RegisterResponse> {
  const base = getBaseUrl();
  const res = await fetch(`${base}/auth/register`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email, password }),
  });
  return handleResponse<RegisterResponse>(res);
}

export async function login(email: string, password: string): Promise<LoginResponse> {
  const base = getBaseUrl();
  const res = await fetch(`${base}/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email, password }),
  });
  return handleResponse<LoginResponse>(res);
}

export async function listDocs(token: string): Promise<DocMeta[]> {
  const base = getBaseUrl();
  const res = await fetch(`${base}/api/docs`, {
    headers: { Authorization: `Bearer ${token}` },
  });
  return handleResponse<DocMeta[]>(res);
}

export async function createDoc(token: string, name: string): Promise<DocMeta> {
  const base = getBaseUrl();
  const res = await fetch(`${base}/api/docs`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${token}`,
    },
    body: JSON.stringify({ name, type: 'grid' }),
  });
  return handleResponse<DocMeta>(res);
}

export async function getDoc(token: string, id: string): Promise<DocMeta> {
  const base = getBaseUrl();
  const res = await fetch(`${base}/api/docs/${encodeURIComponent(id)}`, {
    headers: { Authorization: `Bearer ${token}` },
  });
  return handleResponse<DocMeta>(res);
}
