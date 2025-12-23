'use client';

import React, { useCallback, useEffect, useRef, useState } from 'react';

type KnowledgeDocumentStatus =
  | 'UPLOADED'
  | 'STORED_ORIGINAL'
  | 'CONVERTED_TO_MARKDOWN'
  | 'PUBLISHED_TO_GITHUB'
  | 'FAILED';

type NonFailedStatus = Exclude<KnowledgeDocumentStatus, 'FAILED'>;

type KnowledgeDocument = {
  id: string;
  status: KnowledgeDocumentStatus;
  path: string;
  repository: string;
  branch: string;
  jobId?: string | null;
  error?: string | null;
};

type KnowledgeDocumentsResponse = {
  knowledgeDocuments: {
    nodes: KnowledgeDocument[];
    totalCount: number;
  };
};

type KnowledgeDocumentResponse = {
  knowledgeDocument: KnowledgeDocument | null;
};

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

export default function DocumentsPage() {
  const [documents, setDocuments] = useState<KnowledgeDocument[]>([]);
  const [documentsTotal, setDocumentsTotal] = useState<number | null>(null);
  const [listError, setListError] = useState<string | null>(null);
  const [isListLoading, setIsListLoading] = useState(false);
  const lastNonFailedStatusRef = useRef<Record<string, NonFailedStatus | undefined>>({});

  const gatewayUrl = process.env.NEXT_PUBLIC_GATEWAY_URL;
  const apiKey = process.env.NEXT_PUBLIC_API_KEY;

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
    if (ids.size === 0) return undefined;

    const interval = setInterval(() => {
      ids.forEach((id) => pollDocument(id));
    }, 5000);

    return () => clearInterval(interval);
  }, [documents, pollDocument]);

  return (
    <main className="px-4 pb-12">
      <div className="mx-auto max-w-6xl space-y-4">
        <div className="flex flex-col gap-2">
          <h1 className="text-2xl font-semibold">Просмотр документов</h1>
          <p className="text-sm text-slate-400">
            Лента последних документов. Список автоматически обновляется, можно принудительно обновить вручную.
          </p>
        </div>

        <div className="rounded-2xl border border-slate-800 bg-slate-900 p-6 shadow-lg shadow-indigo-500/5">
          <div className="flex items-center justify-between">
            <h2 className="text-lg font-semibold">Документы</h2>
            <button
              type="button"
              onClick={fetchDocuments}
              disabled={isListLoading}
              className="inline-flex items-center rounded-lg bg-slate-800 px-3 py-2 text-sm hover:bg-slate-700 disabled:cursor-not-allowed disabled:opacity-60"
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
          <p className="break-all font-mono text-sm text-indigo-200">{document.path}</p>
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
