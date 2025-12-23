'use client';

import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';

type UploadStatus = 'idle' | 'uploading' | 'done' | 'error';

type KnowledgeDocumentStatus =
  | 'UPLOADED'
  | 'STORED_ORIGINAL'
  | 'CONVERTED_TO_MARKDOWN'
  | 'PUBLISHED_TO_GITHUB'
  | 'FAILED';

interface KnowledgeDocument {
  id: string;
  status: KnowledgeDocumentStatus;
  path: string;
  repository: string;
  branch: string;
  jobId?: string | null;
  error?: string | null;
}

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

interface KnowledgeDocumentsResponse {
  knowledgeDocuments: {
    nodes: KnowledgeDocument[];
    totalCount: number;
  };
}

interface KnowledgeDocumentResponse {
  knowledgeDocument: KnowledgeDocument | null;
}

const KNOWLEDGE_ROOT = 'knowledge/docs';
type NonFailedStatus = Exclude<KnowledgeDocumentStatus, 'FAILED'>;

const STATUS_SEQUENCE: NonFailedStatus[] = [
  'UPLOADED',
  'STORED_ORIGINAL',
  'CONVERTED_TO_MARKDOWN',
  'PUBLISHED_TO_GITHUB',
];

const STATUS_META: Record<NonFailedStatus, { label: string; description: string; icon: string }> = {
  UPLOADED: {
    label: 'Файл принят',
    description: 'Файл получен и ожидает сохранения',
    icon: '⬆️',
  },
  STORED_ORIGINAL: {
    label: 'Оригинал сохранён',
    description: 'Исходный файл положен в хранилище',
    icon: '🗄️',
  },
  CONVERTED_TO_MARKDOWN: {
    label: 'Преобразован в Markdown',
    description: 'Файл сконвертирован и готов к публикации',
    icon: '📄',
  },
  PUBLISHED_TO_GITHUB: {
    label: 'Опубликован в GitHub',
    description: 'Markdown отправлен в репозиторий',
    icon: '🚀',
  },
};

const generateCorrelationId = () =>
  typeof crypto !== 'undefined' && 'randomUUID' in crypto
    ? crypto.randomUUID()
    : `corr-${Date.now()}`;

export default function HomePage() {
  const [file, setFile] = useState<File | null>(null);
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
  const [documents, setDocuments] = useState<KnowledgeDocument[]>([]);
  const [documentsTotal, setDocumentsTotal] = useState<number | null>(null);
  const [listError, setListError] = useState<string | null>(null);
  const [isListLoading, setIsListLoading] = useState(false);
  const lastNonFailedStatusRef = useRef<Record<string, NonFailedStatus | undefined>>({});

  const gatewayUrl = process.env.NEXT_PUBLIC_GATEWAY_URL;
  const apiKey = process.env.NEXT_PUBLIC_API_KEY;
  const repository = process.env.NEXT_PUBLIC_DEFAULT_REPOSITORY ?? 'docs';
  const branch = process.env.NEXT_PUBLIC_DEFAULT_BRANCH ?? 'main';

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

  const fetchDocuments = useCallback(async () => {
    if (!gatewayUrl || !apiKey) {
      setListError('NEXT_PUBLIC_GATEWAY_URL или NEXT_PUBLIC_API_KEY не заданы');
      return;
    }

    try {
      setIsListLoading(true);
      setListError(null);
      const correlationId = generateCorrelationId();

      const query = `
        query KnowledgeDocuments($pagination: Pagination) {
          knowledgeDocuments(pagination: $pagination) {
            nodes {
              id
              status
              path
              repository
              branch
              jobId
              error
            }
            totalCount
          }
        }
      `;

      const res = await fetch(gatewayUrl, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'X-API-Key': apiKey,
          'X-Correlation-ID': correlationId,
        },
        body: JSON.stringify({
          query,
          variables: { pagination: { limit: 20, offset: 0 } },
        }),
      });

      if (!res.ok) {
        throw new Error(`Ошибка загрузки списка документов: ${res.status} ${res.statusText}`);
      }

      const json = (await res.json()) as { data?: KnowledgeDocumentsResponse; errors?: { message: string }[] };

      if (json.errors?.length) {
        throw new Error(json.errors.map((e) => e.message).join('; '));
      }

      const payload = json.data?.knowledgeDocuments;
      if (!payload) {
        throw new Error('Пустой ответ knowledgeDocuments');
      }

      setDocuments(payload.nodes);
      setDocumentsTotal(payload.totalCount);
      payload.nodes.forEach((doc) => {
        if (doc.status !== 'FAILED') {
          lastNonFailedStatusRef.current[doc.id] = doc.status;
        }
      });
    } catch (err) {
      const messageText = err instanceof Error ? err.message : 'Не удалось загрузить список документов';
      console.error(messageText);
      setListError(messageText);
    } finally {
      setIsListLoading(false);
    }
  }, [apiKey, gatewayUrl]);

  const pollDocument = useCallback(
    async (id: string) => {
      if (!gatewayUrl || !apiKey) {
        return;
      }

      try {
        const correlationId = generateCorrelationId();
        const query = `
          query KnowledgeDocument($id: ID!) {
            knowledgeDocument(id: $id) {
              id
              status
              path
              repository
              branch
              jobId
              error
            }
          }
        `;

        const res = await fetch(gatewayUrl, {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json',
            'X-API-Key': apiKey,
            'X-Correlation-ID': correlationId,
          },
          body: JSON.stringify({ query, variables: { id } }),
        });

        if (!res.ok) {
          throw new Error(`Ошибка polling документа ${id}: ${res.status} ${res.statusText}`);
        }

        const json = (await res.json()) as { data?: KnowledgeDocumentResponse; errors?: { message: string }[] };
        if (json.errors?.length) {
          throw new Error(json.errors.map((e) => e.message).join('; '));
        }

        const doc = json.data?.knowledgeDocument;
        if (!doc) {
          return;
        }

        setDocuments((prev) =>
          prev.map((item) => {
            if (item.id !== doc.id) return item;
            if (doc.status !== 'FAILED') {
              lastNonFailedStatusRef.current[doc.id] = doc.status;
            }
            return { ...item, ...doc };
          }),
        );
      } catch (err) {
        console.error(err);
      }
    },
    [apiKey, gatewayUrl],
  );

  useEffect(() => {
    fetchDocuments();
  }, [fetchDocuments]);

  useEffect(() => {
    const ids = new Set(documents.map((doc) => doc.id));
    if (documentId) {
      ids.add(documentId);
    }
    if (ids.size === 0) return undefined;

    const interval = setInterval(() => {
      ids.forEach((id) => pollDocument(id));
    }, 5000);

    return () => clearInterval(interval);
  }, [documents, documentId, pollDocument]);

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
      const correlationId = generateCorrelationId();

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
      fetchDocuments();
    } catch (err: unknown) {
      console.error(err);
      setStatus('error');
      const errorMessage = err instanceof Error ? err.message : 'Неизвестная ошибка';
      setMessage(errorMessage);
    }
  };

  return (
      <main className="min-h-screen bg-slate-950 text-slate-100 flex items-start justify-center">
        <div className="w-full max-w-5xl mx-auto grid gap-6 p-6 lg:grid-cols-2">
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

          <div className="w-full p-6 bg-slate-900 rounded-2xl shadow-lg border border-slate-800">
            <div className="flex items-center justify-between">
              <h2 className="text-xl font-semibold">Документы</h2>
              <button
                type="button"
                onClick={fetchDocuments}
                disabled={isListLoading}
                className="inline-flex items-center rounded-lg bg-slate-800 px-3 py-2 text-sm hover:bg-slate-700 disabled:opacity-60"
              >
                Обновить
              </button>
            </div>

            {listError && (
              <div className="mt-4 rounded-lg border border-red-500 bg-red-950/40 px-3 py-2 text-sm text-red-200">
                {listError}
              </div>
            )}

            <div className="mt-4 space-y-4">
              {isListLoading && <p className="text-sm text-slate-400">Загружаем список…</p>}
              {!isListLoading && documents.length === 0 && (
                <p className="text-sm text-slate-400">Документы пока не загружались.</p>
              )}

              {documents.map((doc) => (
                <DocumentCard
                  key={doc.id}
                  document={doc}
                  lastNonFailedStatus={lastNonFailedStatusRef.current[doc.id]}
                />
              ))}
            </div>

            {typeof documentsTotal === 'number' && (
              <p className="mt-4 text-xs text-slate-500">Всего документов: {documentsTotal}</p>
            )}
          </div>
        </div>
      </main>
  );
}

function DocumentCard({
  document,
  lastNonFailedStatus,
}: {
  document: KnowledgeDocument;
  lastNonFailedStatus?: NonFailedStatus;
}) {
  const currentStatus = document.status;
  const finalStatus: NonFailedStatus =
    currentStatus === 'FAILED' ? lastNonFailedStatus ?? STATUS_SEQUENCE[0] : currentStatus;

  const getStageState = (stage: NonFailedStatus) => {
    if (currentStatus === 'FAILED') {
      if (stage === finalStatus) return 'error';
      if (STATUS_SEQUENCE.indexOf(stage) <= STATUS_SEQUENCE.indexOf(finalStatus)) return 'completed';
      return 'upcoming';
    }

    const stageIndex = STATUS_SEQUENCE.indexOf(stage);
    const currentIndex = STATUS_SEQUENCE.indexOf(currentStatus);
    if (stageIndex < currentIndex) return 'completed';
    if (stageIndex === currentIndex) return 'active';
    return 'upcoming';
  };

  return (
    <div className="rounded-xl border border-slate-800 bg-slate-900/50 p-4">
      <div className="flex items-start justify-between gap-4">
        <div>
          <p className="text-sm text-slate-400">Путь</p>
          <p className="font-mono text-sm text-indigo-200 break-all">{document.path}</p>
          <p className="mt-1 text-xs text-slate-500">
            Репозиторий: <span className="font-mono">{document.repository}</span> / ветка:{' '}
            <span className="font-mono">{document.branch}</span>
          </p>
          {document.jobId && (
            <p className="text-xs text-slate-500">
              jobId: <span className="font-mono">{document.jobId}</span>
            </p>
          )}
        </div>
        <span
          className={`rounded-full px-3 py-1 text-xs font-medium ${
            currentStatus === 'FAILED' ? 'bg-red-900/60 text-red-200' : 'bg-slate-800 text-slate-200'
          }`}
        >
          {currentStatus === 'FAILED' ? 'Ошибка' : STATUS_META[currentStatus]?.label ?? currentStatus}
        </span>
      </div>

      <div className="mt-4 space-y-3">
        {STATUS_SEQUENCE.map((stage, index) => {
          const state = getStageState(stage);
          const meta = STATUS_META[stage];
          const isLast = index === STATUS_SEQUENCE.length - 1;
          const lineColor =
            state === 'completed' ? 'bg-emerald-500' : state === 'active' ? 'bg-indigo-500' : 'bg-slate-700';

          return (
            <div key={stage} className="flex items-start gap-3">
              <div className="flex flex-col items-center">
                <div
                  className={`flex h-9 w-9 items-center justify-center rounded-full border text-lg ${
                    state === 'completed'
                      ? 'border-emerald-500 bg-emerald-500/20 text-emerald-200'
                      : state === 'active'
                        ? 'border-indigo-500 bg-indigo-500/20 text-indigo-200'
                        : 'border-slate-700 bg-slate-800 text-slate-400'
                  }`}
                >
                  {meta.icon}
                </div>
                {!isLast && <div className={`mt-1 h-8 w-px ${lineColor}`} aria-hidden />}
              </div>
              <div className="flex-1">
                <p className="text-sm font-medium text-slate-100">{meta.label}</p>
                <p className="text-xs text-slate-400">{meta.description}</p>
                {state === 'active' && currentStatus !== 'FAILED' && (
                  <p className="mt-1 text-xs text-indigo-200">Статус обновляется…</p>
                )}
              </div>
            </div>
          );
        })}
        {currentStatus === 'FAILED' && (
          <div className="flex items-start gap-3">
            <div className="flex flex-col items-center">
              <div className="flex h-9 w-9 items-center justify-center rounded-full border border-red-500 bg-red-500/20 text-red-200">
                ⚠️
              </div>
            </div>
            <div className="flex-1">
              <p className="text-sm font-medium text-red-200">Процесс завершился с ошибкой</p>
              <p className="text-xs text-red-300">
                {document.error ? document.error : 'Неизвестная ошибка обработки'}
              </p>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
