'use client';

import React, { useState } from 'react';

type UploadStatus = 'idle' | 'uploading' | 'done' | 'error';

interface UploadResponse {
  data?: {
    uploadKnowledgeDocument?: {
      document: {
        id: string;
        status: string;
        repository: string;
        branch: string;
        path: string;
      };
      uploadUrl: string;
      uploadMethod: string;
    };
  };
  errors?: { message: string }[];
}

export default function HomePage() {
  const [file, setFile] = useState<File | null>(null);
  const [repository, setRepository] = useState('docs');
  const [branch, setBranch] = useState('main');
  const [path, setPath] = useState('knowledge/docs/test.md');

  const [status, setStatus] = useState<UploadStatus>('idle');
  const [message, setMessage] = useState<string | null>(null);
  const [documentId, setDocumentId] = useState<string | null>(null);

  const gatewayUrl = process.env.NEXT_PUBLIC_GATEWAY_URL;
  const apiKey = process.env.NEXT_PUBLIC_API_KEY;

  console.log('GATEWAY URL:', gatewayUrl);
  const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const f = e.target.files?.[0] ?? null;
    setFile(f);
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setMessage(null);

    if (!file) {
      setMessage('Выберите файл');
      return;
    }
    if (!gatewayUrl || !apiKey) {
      setMessage('NEXT_PUBLIC_GATEWAY_URL или NEXT_PUBLIC_API_KEY не заданы');
      return;
    }

    try {
      setStatus('uploading');

      // 1) Генерируем correlation-id на фронте
      const correlationId =
          typeof crypto !== 'undefined' && 'randomUUID' in crypto
              ? crypto.randomUUID()
              : `corr-${Date.now()}`;

      // 2) GraphQL мутация uploadKnowledgeDocument
      const mutation = `
        mutation UploadDoc($input: UploadKnowledgeDocumentInput!) {
          uploadKnowledgeDocument(input: $input) {
            document {
              id
              status
              repository
              branch
              path
            }
            uploadUrl
            uploadMethod
          }
        }
      `;

      const variables = {
        input: {
          fileName: file.name,
          fileSizeBytes: file.size,
          repository,
          branch,
          path,
        },
      };

      const gqlResp = await fetch(gatewayUrl, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'X-API-Key': apiKey,
          'X-Correlation-ID': correlationId,
        },
        body: JSON.stringify({ query: mutation, variables }),
      });

      const gqlJson: UploadResponse = await gqlResp.json();

      if (gqlJson.errors && gqlJson.errors.length > 0) {
        throw new Error(gqlJson.errors.map((e) => e.message).join('; '));
      }

      const payload = gqlJson.data?.uploadKnowledgeDocument;
      if (!payload) {
        throw new Error('Пустой ответ uploadKnowledgeDocument');
      }

      const { document, uploadUrl, uploadMethod } = payload;
      setDocumentId(document.id);

      if (!uploadUrl) {
        throw new Error('uploadUrl пустой');
      }
      if (uploadMethod.toUpperCase() !== 'PUT') {
        throw new Error(`Ожидался PUT, а пришёл ${uploadMethod}`);
      }

      // 3) PUT файла в MinIO по presigned URL
      const putResp = await fetch(uploadUrl, {
        method: 'PUT',
        headers: {
          // Важно: должен совпасть с тем, что использовался при подписи
          'x-amz-meta-correlation-id': correlationId,
          'Content-Type':
              file.type ||
              'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
        },
        body: file,
      });

      if (!putResp.ok) {
        const text = await putResp.text();
        throw new Error(
            `Ошибка при загрузке файла в MinIO: ${putResp.status} ${putResp.statusText}\n${text}`,
        );
      }

      setStatus('done');
      setMessage(
          `Файл загружен. Document ID: ${document.id}, статус: ${document.status}`,
      );
    } catch (err: unknown) {
      console.error(err);
      setStatus('error');
      const errorMessage = err instanceof Error ? err.message : 'Неизвестная ошибка';
      setMessage(errorMessage);
    }
  };

  return (
      <main className="min-h-screen bg-slate-950 text-slate-100 flex items-center justify-center">
        <div className="w-full max-w-xl mx-auto p-6 bg-slate-900 rounded-2xl shadow-lg border border-slate-800">
          <h1 className="text-2xl font-semibold mb-4">
            Загрузка DOC/DOCX/PDF в knowledge-base
          </h1>

          <form onSubmit={handleSubmit} className="space-y-4">
            <div>
              <label className="block text-sm mb-1">Файл</label>
              <input
                  type="file"
                  accept=".doc,.docx,.pdf"
                  onChange={handleFileChange}
                  className="block w-full text-sm text-slate-100 file:mr-4 file:py-2 file:px-4 file:rounded-lg file:border-0 file:text-sm file:font-semibold file:bg-indigo-500 file:text-white hover:file:bg-indigo-400 cursor-pointer"
              />
              {file && (
                  <p className="mt-1 text-xs text-slate-400">
                    {file.name} ({file.size} байт)
                  </p>
              )}
            </div>

            <div className="grid grid-cols-1 gap-3">
              <div>
                <label className="block text-sm mb-1">Название репозитория</label>
                <input
                    type="text"
                    value={repository}
                    onChange={(e) => setRepository(e.target.value)}
                    placeholder="например: docs"
                    className="w-full rounded-lg border border-slate-700 bg-slate-800 px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-indigo-500"
                />
                <p className="mt-1 text-xs text-slate-400">
                  Укажите только имя репозитория без владельца (например, <code>docs</code>).
                </p>
              </div>
              <div>
                <label className="block text-sm mb-1">Branch</label>
                <input
                    type="text"
                    value={branch}
                    onChange={(e) => setBranch(e.target.value)}
                    className="w-full rounded-lg border border-slate-700 bg-slate-800 px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-indigo-500"
                />
              </div>
              <div>
                <label className="block text-sm mb-1">Path в репо</label>
                <input
                    type="text"
                    value={path}
                    onChange={(e) => setPath(e.target.value)}
                    className="w-full rounded-lg border border-slate-700 bg-slate-800 px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-indigo-500"
                />
              </div>
            </div>

            <button
                type="submit"
                disabled={status === 'uploading'}
                className="mt-2 inline-flex items-center justify-center rounded-lg bg-indigo-500 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-400 disabled:opacity-60 disabled:cursor-not-allowed"
            >
              {status === 'uploading' ? 'Загружаю…' : 'Загрузить документ'}
            </button>
          </form>

          {message && (
              <div
                  className={`mt-4 rounded-lg border px-3 py-2 text-sm ${
                      status === 'error'
                          ? 'border-red-500 text-red-400 bg-red-950/40'
                          : 'border-emerald-500 text-emerald-400 bg-emerald-950/40'
                  }`}
              >
                {message}
              </div>
          )}

          {documentId && (
              <p className="mt-2 text-xs text-slate-500">
                ID документа: <span className="font-mono">{documentId}</span>
              </p>
          )}
        </div>
      </main>
  );
}
