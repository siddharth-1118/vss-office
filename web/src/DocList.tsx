import React, { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useAuth } from './auth';
import { type DocMeta, listDocs, createDoc } from './api';

export const DocList: React.FC = () => {
  const { token, user, logout } = useAuth();
  const [docs, setDocs] = useState<DocMeta[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const navigate = useNavigate();

  const loadDocuments = async () => {
    if (!token) return;
    try {
      setLoading(true);
      setError(null);
      const data = await listDocs(token);
      setDocs(data);
    } catch (err: any) {
      setError(err?.message || String(err));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadDocuments();
  }, [token]);

  const handleCreateDoc = async () => {
    if (!token) return;
    try {
      setCreating(true);
      const name = `Sheet ${docs.length + 1}`;
      const newDoc = await createDoc(token, name);
      navigate(`/docs/${newDoc.id}`);
    } catch (err: any) {
      setError(err?.message || String(err));
    } finally {
      setCreating(false);
    }
  };

  return (
    <div className="min-h-screen bg-gray-50">
      <header className="bg-white border-b border-gray-200">
        <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 h-16 flex items-center justify-between">
          <div className="flex items-center space-x-3">
            <span className="text-xl font-bold text-gray-900">VSS Office</span>
            <span className="text-sm bg-blue-100 text-blue-800 px-2 py-0.5 rounded font-medium">
              Sheets
            </span>
          </div>
          <div className="flex items-center space-x-4">
            <span className="text-sm text-gray-600">{user?.email}</span>
            <button
              onClick={logout}
              className="text-sm text-gray-500 hover:text-gray-700 font-medium"
            >
              Sign out
            </button>
          </div>
        </div>
      </header>

      <main className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
        <div className="flex items-center justify-between mb-6">
          <h1 className="text-2xl font-bold text-gray-900">Documents</h1>
          <button
            id="new-sheet-btn"
            onClick={handleCreateDoc}
            disabled={creating}
            className="inline-flex items-center px-4 py-2 border border-transparent text-sm font-medium rounded-md shadow-sm text-white bg-blue-600 hover:bg-blue-700 focus:outline-none focus:ring-2 focus:ring-offset-2 focus:ring-blue-500 disabled:opacity-50"
          >
            {creating ? 'Creating...' : '+ New Sheet'}
          </button>
        </div>

        {error && (
          <div className="mb-6 p-4 bg-red-50 border border-red-200 text-red-700 text-sm rounded-md">
            {error}
          </div>
        )}

        {loading ? (
          <div className="text-center py-12 text-gray-500">Loading documents...</div>
        ) : docs.length === 0 ? (
          <div className="text-center py-12 bg-white rounded-lg border border-dashed border-gray-300">
            <p className="text-gray-500">No documents yet. Create your first sheet to begin.</p>
          </div>
        ) : (
          <div className="bg-white shadow overflow-hidden sm:rounded-md border border-gray-200">
            <ul className="divide-y divide-gray-200">
              {docs.map((doc) => (
                <li
                  key={doc.id}
                  onClick={() => navigate(`/docs/${doc.id}`)}
                  className="px-6 py-4 hover:bg-gray-50 cursor-pointer flex items-center justify-between transition-colors"
                >
                  <div className="flex items-center space-x-3">
                    <span className="text-blue-600">📊</span>
                    <div>
                      <p className="text-sm font-medium text-gray-900">{doc.name}</p>
                      <p className="text-xs text-gray-500">
                        Created {new Date(doc.created_at).toLocaleString()}
                      </p>
                    </div>
                  </div>
                  <span className="text-xs font-semibold uppercase tracking-wider text-gray-400 bg-gray-100 px-2 py-1 rounded">
                    {doc.type}
                  </span>
                </li>
              ))}
            </ul>
          </div>
        )}
      </main>
    </div>
  );
};
