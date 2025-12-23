'use client';

import Link from 'next/link';
import React, { useCallback, useEffect, useState } from 'react';

type KnowledgeDocumentStatus =
  | 'UPLOADED'
  | 'STORED_ORIGINAL'
  | 'CONVERTED_TO_MARKDOWN'
  | 'PUBLISHED_TO_GITHUB'
  | 'FAILED';

type KnowledgeDocument = {
  id: string;
  status: KnowledgeDocumentStatus;
  path: string;
  repository: string;
  branch: string;
  jobId?: string | null;
  error?: string | null;
  fileName: string;
  createdAt: string;
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

const STATUS_LABELS: Record<KnowledgeDocumentStatus, string> = {
  UPLOADED: 'Файл принят',
  STORED_ORIGINAL: 'Оригинал сохранён',
  CONVERTED_TO_MARKDOWN: 'Преобразован в Markdown',
  PUBLISHED_TO_GITHUB: 'Опубликован в GitHub',
  FAILED: 'Ошибка обработки',
};

const STATUS_STYLES: Record<KnowledgeDocumentStatus, string> = {
  UPLOADED: 'bg-slate-800 text-slate-100 border-slate-700',
  STORED_ORIGINAL: 'bg-blue-900/40 text-blue-100 border-blue-700/60',
  CONVERTED_TO_MARKDOWN: 'bg-indigo-900/40 text-indigo-100 border-indigo-700/60',
  PUBLISHED_TO_GITHUB: 'bg-emerald-900/40 text-emerald-100 border-emerald-700/60',
  FAILED: 'bg-red-900/40 text-red-100 border-red-700/60',
};

const generateCorrelationId = () =>
  typeof crypto !== 'undefined' && 'randomUUID' in crypto
    ? crypto.randomUUID()
    : `corr-${Date.now()}`;

const formatUploadDate = (value: string) => {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return '—';
  }

  return new Intl.DateTimeFormat('ru-RU', {
    day: '2-digit',
    month: '2-digit',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
    .format(date)
    .replace(',', '');
};

const getDocumentName = (document: KnowledgeDocument) => {
  if (document.fileName?.trim()) {
    return document.fileName.trim();
  }
  const parts = document.path.split('/').filter(Boolean);
  return parts[parts.length - 1] || document.path;
};

export default function DocumentsPage() {
  const [documents, setDocuments] = useState<KnowledgeDocument[]>([]);
  const [documentsTotal, setDocumentsTotal] = useState<number | null>(null);
  const [listError, setListError] = useState<string | null>(null);
  const [isListLoading, setIsListLoading] = useState(false);

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
              fileName
              createdAt
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
              fileName
              createdAt
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

        setDocuments((prev) => prev.map((item) => (item.id === doc.id ? { ...item, ...doc } : item)));
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
          <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
            <h2 className="text-lg font-semibold">Документы</h2>
            <button
              type="button"
              onClick={fetchDocuments}
              disabled={isListLoading}
              className="inline-flex items-center justify-center rounded-lg bg-slate-800 px-3 py-2 text-sm hover:bg-slate-700 disabled:cursor-not-allowed disabled:opacity-60"
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

            {documents.length > 0 && (
              <>
                <div className="hidden md:block">
                  <DocumentsTable documents={documents} />
                </div>
                <div className="grid gap-3 md:hidden">
                  {documents.map((doc) => (
                    <DocumentCard key={doc.id} document={doc} />
                  ))}
                </div>
              </>
            )}
          </div>

          {typeof documentsTotal === 'number' && (
            <p className="mt-4 text-xs text-slate-500">Всего документов: {documentsTotal}</p>
          )}
        </div>
      </div>
    </main>
  );
}

function DocumentsTable({ documents }: { documents: KnowledgeDocument[] }) {
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead className="w-[40%] min-w-[200px]">Название</TableHead>
          <TableHead className="w-[20%]">Статус</TableHead>
          <TableHead className="w-[20%]">Дата загрузки</TableHead>
          <TableHead className="w-[20%]" />
        </TableRow>
      </TableHeader>
      <TableBody>
        {documents.map((document) => (
          <TableRow key={document.id}>
            <TableCell>
              <p className="font-medium text-slate-100">{getDocumentName(document)}</p>
              <p className="text-xs text-slate-400">
                <span className="font-mono text-slate-300">{document.repository}</span> / ветка{' '}
                <span className="font-mono text-slate-300">{document.branch}</span>
              </p>
              <p className="text-xs text-slate-500">Путь: {document.path}</p>
              {document.jobId && (
                <p className="text-xs text-slate-500">
                  jobId: <span className="font-mono">{document.jobId}</span>
                </p>
              )}
            </TableCell>
            <TableCell>
              <StatusBadge status={document.status} />
              {document.status === 'FAILED' && document.error && (
                <p className="mt-1 text-xs text-red-200">{document.error}</p>
              )}
            </TableCell>
            <TableCell className="whitespace-nowrap text-slate-200">{formatUploadDate(document.createdAt)}</TableCell>
            <TableCell className="text-right">
              <OpenButton document={document} />
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

function DocumentCard({ document }: { document: KnowledgeDocument }) {
  return (
    <div className="rounded-xl border border-slate-800 bg-slate-900/50 p-4 shadow-sm">
      <div className="flex items-start justify-between gap-3">
        <div>
          <p className="text-sm font-semibold text-slate-100">{getDocumentName(document)}</p>
          <p className="text-xs text-slate-500">
            <span className="font-mono text-slate-300">{document.repository}</span> • ветка{' '}
            <span className="font-mono text-slate-300">{document.branch}</span>
          </p>
          <p className="mt-1 text-xs text-slate-500">Путь: {document.path}</p>
        </div>
        <StatusBadge status={document.status} />
      </div>

      <div className="mt-3 flex flex-wrap items-center gap-2 text-xs text-slate-300">
        <span className="text-slate-400">Загружено:</span>
        <span className="font-mono text-slate-200">{formatUploadDate(document.createdAt)}</span>
        {document.jobId && (
          <span className="ml-auto rounded-full bg-slate-800 px-2 py-1 font-mono text-[10px] text-slate-200">
            jobId: {document.jobId}
          </span>
        )}
      </div>

      {document.status === 'FAILED' && document.error && (
        <p className="mt-2 text-xs text-red-200">Ошибка: {document.error}</p>
      )}

      <div className="mt-3 flex justify-end">
        <OpenButton document={document} />
      </div>
    </div>
  );
}

function OpenButton({ document }: { document: KnowledgeDocument }) {
  return (
    <Link
      href={`/documents/${document.id}`}
      className={cn(
        'inline-flex items-center justify-center rounded-lg bg-indigo-600 px-3 py-2 text-sm font-medium text-white shadow hover:bg-indigo-500 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-indigo-400',
        'min-w-[120px]',
      )}
    >
      Открыть
    </Link>
  );
}

function StatusBadge({ status }: { status: KnowledgeDocumentStatus }) {
  return (
    <span className={cn('inline-flex items-center gap-2 rounded-full border px-3 py-1 text-xs font-medium', STATUS_STYLES[status])}>
      <span className="h-2 w-2 rounded-full bg-current opacity-80" aria-hidden />
      {STATUS_LABELS[status] ?? status}
    </span>
  );
}

function cn(...classes: Array<string | false | null | undefined>) {
  return classes.filter(Boolean).join(' ');
}

function Table({ children }: { children: React.ReactNode }) {
  return (
    <div className="w-full overflow-x-auto">
      <table className="w-full caption-bottom text-sm">{children}</table>
    </div>
  );
}

function TableHeader({ children }: { children: React.ReactNode }) {
  return <thead className="[&_tr]:border-b [&_tr]:border-slate-800">{children}</thead>;
}

function TableBody({ children }: { children: React.ReactNode }) {
  return <tbody className="[&_tr:last-child]:border-0">{children}</tbody>;
}

function TableRow({ children }: { children: React.ReactNode }) {
  return <tr className="border-b border-slate-800 transition hover:bg-slate-800/50">{children}</tr>;
}

function TableHead({ children, className }: { children: React.ReactNode; className?: string }) {
  return (
    <th className={cn('h-12 px-4 text-left align-middle text-xs font-semibold text-slate-400', className)} scope="col">
      {children}
    </th>
  );
}

function TableCell({ children, className }: { children: React.ReactNode; className?: string }) {
  return <td className={cn('p-4 align-top text-sm text-slate-200', className)}>{children}</td>;
}
