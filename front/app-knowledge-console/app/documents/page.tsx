'use client';

import Link from 'next/link';
import { signIn } from 'next-auth/react';
import React, { useCallback, useEffect, useState } from 'react';
import { generateCorrelationId } from '@/lib/correlation';
import { SilentSignInError, fetchWithAuth } from '@/lib/http-client';

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
  UPLOADED: 'bg-slate-50 text-slate-800 border-slate-300',
  STORED_ORIGINAL: 'bg-blue-50 text-blue-800 border-blue-300',
  CONVERTED_TO_MARKDOWN: 'bg-indigo-50 text-indigo-800 border-indigo-300',
  PUBLISHED_TO_GITHUB: 'bg-emerald-50 text-emerald-800 border-emerald-300',
  FAILED: 'bg-red-50 text-red-800 border-red-300',
};

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
  const [manualLoginRequired, setManualLoginRequired] = useState(false);

  const fetchDocuments = useCallback(async () => {
    try {
      setIsListLoading(true);
      setListError(null);
      setManualLoginRequired(false);
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

      const res = await fetchWithAuth('/api/graphql', {
        method: 'POST',
        cache: 'no-store',
        headers: {
          'Content-Type': 'application/json',
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
      if (err instanceof SilentSignInError) {
        setManualLoginRequired(true);
        const friendlyMessage = err.interactionRequired
          ? 'Сессия истекла. Войдите вручную, чтобы продолжить.'
          : err.message;
        setListError(friendlyMessage);
        return;
      }
      const messageText = err instanceof Error ? err.message : 'Не удалось загрузить список документов';
      console.error(messageText);
      setListError(messageText);
    } finally {
      setIsListLoading(false);
    }
  }, []);

  const pollDocument = useCallback(
    async (id: string) => {
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

        const res = await fetchWithAuth('/api/graphql', {
          method: 'POST',
          cache: 'no-store',
          headers: {
            'Content-Type': 'application/json',
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
        if (err instanceof SilentSignInError) {
          setManualLoginRequired(true);
          console.warn('Polling остановлен: требуется ручной вход');
          return;
        }
        console.error(err);
      }
    },
    [],
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
          <h1 className="text-2xl font-semibold text-slate-900">Просмотр документов</h1>
          <p className="text-sm text-slate-600">
            Лента последних документов. Список автоматически обновляется, можно принудительно обновить вручную.
          </p>
        </div>

        <div className="rounded-2xl border border-slate-200 bg-white p-6 shadow-sm">
          <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
            <h2 className="text-lg font-semibold text-slate-900">Документы</h2>
            {manualLoginRequired && (
              <div className="flex flex-col items-start gap-2 sm:flex-row sm:items-center">
                <button
                  type="button"
                  onClick={() => signIn('authentik', { callbackUrl: window.location.href })}
                  className="inline-flex items-center justify-center rounded-lg border border-indigo-200 bg-indigo-50 px-3 py-2 text-xs font-medium text-indigo-700 transition hover:bg-indigo-100"
                >
                  Войти вручную
                </button>
                <p className="text-xs text-slate-600">
                  Silent sign-in потребовал взаимодействие. Авторизуйтесь вручную и повторите запрос.
                </p>
              </div>
            )}
            <button
              type="button"
              onClick={fetchDocuments}
              disabled={isListLoading}
              className="inline-flex items-center justify-center rounded-lg bg-indigo-600 px-3 py-2 text-sm font-medium text-white shadow-sm transition hover:bg-indigo-500 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-indigo-400 disabled:cursor-not-allowed disabled:opacity-60"
            >
              Обновить
            </button>
          </div>

          {listError && (
            <div className="mt-4 rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-800">
              {listError}
            </div>
          )}

          <div className="mt-4 space-y-4">
            {isListLoading && <p className="text-sm text-slate-600">Загружаем список…</p>}
            {!isListLoading && documents.length === 0 && <p className="text-sm text-slate-600">Документы пока не загружались.</p>}

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
          <TableHead className="w-[20%] text-right">
            <span className="sr-only">Действия</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {documents.map((document) => (
          <TableRow key={document.id}>
            <TableCell>
              <p className="font-medium text-slate-900">{getDocumentName(document)}</p>
              <p className="text-xs text-slate-700">
                <span className="font-mono text-slate-700">{document.repository}</span> / ветка{' '}
                <span className="font-mono text-slate-700">{document.branch}</span>
              </p>
              <p className="text-xs text-slate-600">Путь: {document.path}</p>
              {document.jobId && (
                <p className="text-xs text-slate-600">
                  jobId: <span className="font-mono">{document.jobId}</span>
                </p>
              )}
            </TableCell>
            <TableCell>
              <StatusBadge status={document.status} />
              {document.status === 'FAILED' && document.error && (
                <p className="mt-1 text-xs text-red-700">{document.error}</p>
              )}
            </TableCell>
            <TableCell className="whitespace-nowrap text-slate-900">{formatUploadDate(document.createdAt)}</TableCell>
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
    <div className="rounded-xl border border-slate-200 bg-white p-4 shadow-sm">
      <div className="flex items-start justify-between gap-3">
        <div>
          <p className="text-sm font-semibold text-slate-900">{getDocumentName(document)}</p>
          <p className="text-xs text-slate-700">
            <span className="font-mono text-slate-700">{document.repository}</span> • ветка{' '}
            <span className="font-mono text-slate-700">{document.branch}</span>
          </p>
          <p className="mt-1 text-xs text-slate-600">Путь: {document.path}</p>
        </div>
        <StatusBadge status={document.status} />
      </div>

      <div className="mt-3 flex flex-wrap items-center gap-2 text-xs text-slate-700">
        <span className="text-slate-600">Загружено:</span>
        <span className="font-mono text-slate-700">{formatUploadDate(document.createdAt)}</span>
        {document.jobId && (
          <span className="ml-auto rounded-full bg-slate-100 px-2 py-1 font-mono text-[10px] text-slate-700">
            jobId: {document.jobId}
          </span>
        )}
      </div>

      {document.status === 'FAILED' && document.error && (
        <p className="mt-2 text-xs text-red-700">Ошибка: {document.error}</p>
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
  return <thead className="[&_tr]:border-b [&_tr]:border-slate-200">{children}</thead>;
}

function TableBody({ children }: { children: React.ReactNode }) {
  return <tbody className="[&_tr:last-child]:border-0">{children}</tbody>;
}

function TableRow({ children }: { children: React.ReactNode }) {
  return <tr className="border-b border-slate-200 transition hover:bg-slate-50">{children}</tr>;
}

function TableHead({ children, className }: { children: React.ReactNode; className?: string }) {
  return (
    <th className={cn('h-12 px-4 text-left align-middle text-xs font-semibold text-slate-600', className)} scope="col">
      {children}
    </th>
  );
}

function TableCell({ children, className }: { children: React.ReactNode; className?: string }) {
  return <td className={cn('p-4 align-top text-sm text-slate-900', className)}>{children}</td>;
}
