'use client';

import React, { useEffect, useMemo, useState } from 'react';

type UploadStatus = 'idle' | 'uploading' | 'done' | 'error';

type Category = 'plenary' | 'bibliography';
type PlenaryDocumentType = 'transcript' | 'voting';
type BibliographyCategory = 'deputies';

interface PathResolution {
  path: string | null;
  error: string | null;
}

interface PlenaryFields {
  sessionNumber: string;
  documentType: PlenaryDocumentType;
  transcriptNumber: string;
  votingQuestionNumber: string;
}

interface BibliographyFields {
  type: BibliographyCategory;
  slug: string;
}

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

const KNOWLEDGE_ROOT = 'knowledge/docs';

export default function HomePage() {
  const [file, setFile] = useState<File | null>(null);
  const [repository, setRepository] = useState('docs');
  const [branch, setBranch] = useState('main');
  const [category, setCategory] = useState<Category>('plenary');
  const [plenaryFields, setPlenaryFields] = useState<PlenaryFields>({
    sessionNumber: '',
    documentType: 'transcript',
    transcriptNumber: '',
    votingQuestionNumber: '',
  });
  const [bibliographyFields, setBibliographyFields] = useState<BibliographyFields>({
    type: 'deputies',
    slug: '',
  });

  const [status, setStatus] = useState<UploadStatus>('idle');
  const [message, setMessage] = useState<string | null>(null);
  const [documentId, setDocumentId] = useState<string | null>(null);
  const [validationError, setValidationError] = useState<string | null>(null);

  const gatewayUrl = process.env.NEXT_PUBLIC_GATEWAY_URL;
  const apiKey = process.env.NEXT_PUBLIC_API_KEY;

  console.log('GATEWAY URL:', gatewayUrl);
  const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const f = e.target.files?.[0] ?? null;
    setFile(f);
  };

  const formatWithLeadingZeros = (value: string) => value.padStart(2, '0');

  const resolvePath = useMemo<PathResolution>(() => {
    const plenaryPath = (): PathResolution => {
      const session = plenaryFields.sessionNumber.trim();
      if (!session) {
        return { path: null, error: 'Введите номер заседания' };
      }

      if (plenaryFields.documentType === 'transcript') {
        const transcript = plenaryFields.transcriptNumber.trim();
        if (!transcript) {
          return { path: null, error: 'Введите номер стенограммы' };
        }
        return {
          path: `${KNOWLEDGE_ROOT}/plenary/${formatWithLeadingZeros(session)}/transcripts/stenogramma-${formatWithLeadingZeros(transcript)}.md`,
          error: null,
        };
      }

      const question = plenaryFields.votingQuestionNumber.trim();
      if (!question) {
        return { path: null, error: 'Введите номер вопроса голосования' };
      }

      return {
        path: `${KNOWLEDGE_ROOT}/plenary/${formatWithLeadingZeros(session)}/votings/question-${formatWithLeadingZeros(question)}.md`,
        error: null,
      };
    };

    const bibliographyPath = (): PathResolution => {
      const slug = bibliographyFields.slug.trim();
      if (!slug) {
        return { path: null, error: 'Укажите идентификатор/slug' };
      }

      if (bibliographyFields.type === 'deputies') {
        return { path: `${KNOWLEDGE_ROOT}/bibliography/deputies/${slug}.md`, error: null };
      }

      return { path: null, error: 'Неизвестная категория библиографии' };
    };

    return category === 'plenary' ? plenaryPath() : bibliographyPath();
  }, [category, bibliographyFields, plenaryFields]);

  useEffect(() => {
    if (resolvePath.path) {
      setValidationError(null);
    }
  }, [resolvePath.path]);

  const updatePlenaryFields = (fields: Partial<PlenaryFields>) => {
    setPlenaryFields((prev) => ({ ...prev, ...fields }));
  };

  const updateBibliographyFields = (fields: Partial<BibliographyFields>) => {
    setBibliographyFields((prev) => ({ ...prev, ...fields }));
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setMessage(null);
    setValidationError(null);

    if (!file) {
      setMessage('Выберите файл');
      return;
    }
    if (!gatewayUrl || !apiKey) {
      setMessage('NEXT_PUBLIC_GATEWAY_URL или NEXT_PUBLIC_API_KEY не заданы');
      return;
    }
    if (!resolvePath.path) {
      setValidationError(resolvePath.error ?? 'Заполните необходимые поля для пути');
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
          path: resolvePath.path,
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
      setMessage(`Файл загружен. Document ID: ${document.id}, статус: ${document.status}`);
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

              <div className="space-y-3">
                <div>
                  <label className="block text-sm mb-1">Категория документа</label>
                  <select
                      value={category}
                      onChange={(e) => setCategory(e.target.value as Category)}
                      className="w-full rounded-lg border border-slate-700 bg-slate-800 px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-indigo-500"
                  >
                    <option value="plenary">Пленарное заседание</option>
                    <option value="bibliography">Библиография</option>
                  </select>
                </div>

                {category === 'plenary' && (
                    <div className="space-y-3 rounded-lg border border-slate-800 bg-slate-900/50 p-3">
                      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
                        <div>
                          <label className="block text-sm mb-1">Номер заседания</label>
                          <input
                              type="text"
                              value={plenaryFields.sessionNumber}
                              onChange={(e) => updatePlenaryFields({ sessionNumber: e.target.value })}
                              placeholder="например: 5"
                              className="w-full rounded-lg border border-slate-700 bg-slate-800 px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-indigo-500"
                          />
                        </div>
                        <div>
                          <label className="block text-sm mb-1">Тип документа</label>
                          <select
                              value={plenaryFields.documentType}
                              onChange={(e) =>
                                  updatePlenaryFields({ documentType: e.target.value as PlenaryDocumentType })
                              }
                              className="w-full rounded-lg border border-slate-700 bg-slate-800 px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-indigo-500"
                          >
                            <option value="transcript">Стенограмма</option>
                            <option value="voting">Протокол голосования</option>
                          </select>
                        </div>
                      </div>

                      {plenaryFields.documentType === 'transcript' ? (
                          <div>
                            <label className="block text-sm mb-1">Номер стенограммы</label>
                            <input
                                type="text"
                                value={plenaryFields.transcriptNumber}
                                onChange={(e) => updatePlenaryFields({ transcriptNumber: e.target.value })}
                                placeholder="например: 3"
                                className="w-full rounded-lg border border-slate-700 bg-slate-800 px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-indigo-500"
                            />
                          </div>
                      ) : (
                          <div>
                            <label className="block text-sm mb-1">Номер вопроса голосования</label>
                            <input
                                type="text"
                                value={plenaryFields.votingQuestionNumber}
                                onChange={(e) =>
                                    updatePlenaryFields({ votingQuestionNumber: e.target.value })
                                }
                                placeholder="например: 12"
                                className="w-full rounded-lg border border-slate-700 bg-slate-800 px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-indigo-500"
                            />
                          </div>
                      )}
                    </div>
                )}

                {category === 'bibliography' && (
                    <div className="space-y-3 rounded-lg border border-slate-800 bg-slate-900/50 p-3">
                      <div>
                        <label className="block text-sm mb-1">Подкатегория</label>
                        <select
                            value={bibliographyFields.type}
                            onChange={(e) => updateBibliographyFields({ type: e.target.value as BibliographyCategory })}
                            className="w-full rounded-lg border border-slate-700 bg-slate-800 px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-indigo-500"
                        >
                          <option value="deputies">Депутаты</option>
                        </select>
                      </div>
                      <div>
                        <label className="block text-sm mb-1">Slug / идентификатор</label>
                        <input
                            type="text"
                            value={bibliographyFields.slug}
                            onChange={(e) => updateBibliographyFields({ slug: e.target.value })}
                            placeholder="например: ivanov-ivan"
                            className="w-full rounded-lg border border-slate-700 bg-slate-800 px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-indigo-500"
                        />
                        <p className="mt-1 text-xs text-slate-400">
                          Итоговый путь будет вида <code>bibliography/deputies/slug.md</code>.
                        </p>
                      </div>
                    </div>
                )}
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

          {validationError && (
              <div className="mt-4 rounded-lg border border-amber-500 bg-amber-950/40 px-3 py-2 text-sm text-amber-200">
                {validationError}
              </div>
          )}

          {resolvePath.path && (
              <div className="mt-4 rounded-lg border border-slate-800 bg-slate-900/60 px-3 py-2">
                <p className="text-xs uppercase text-slate-400">Предпросмотр пути</p>
                <p className="font-mono text-sm text-indigo-200">{resolvePath.path}</p>
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
