'use client';

import Link from 'next/link';
import React, { useCallback, useEffect, useState } from 'react';
import { generateCorrelationId } from '@/lib/correlation';
import { SilentSignInError, fetchWithAuth } from '@/lib/http-client';
import { handleAuthError } from '@/lib/handle-auth-error';
import { createLogger } from '@/lib/logger';
import { useSessionRestorationState } from '@/lib/session-restoration-store';

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

const POLLABLE_STATUSES: KnowledgeDocumentStatus[] = ['UPLOADED', 'STORED_ORIGINAL', 'CONVERTED_TO_MARKDOWN'];
const MAX_POLL_ERRORS = 3;

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
  const [reauthInProgress, setReauthInProgress] = useState(false);
  const [redirectingToSignIn, setRedirectingToSignIn] = useState(false);
  const [page, setPage] = useState(1);
  const [limit, setLimit] = useState(20);
  const pollingIntervalRef = React.useRef<ReturnType<typeof setInterval> | null>(null);
  const pollErrorAttempts = React.useRef<Map<string, number>>(new Map());
  const restoringSession = useSessionRestorationState();
  const showRestoringOverlay = restoringSession || reauthInProgress;

  const stopPolling = useCallback(() => {
    if (pollingIntervalRef.current) {
      clearInterval(pollingIntervalRef.current);
      pollingIntervalRef.current = null;
    }
  }, []);

  useEffect(() => {
    if (restoringSession) {
      setReauthInProgress(true);
      return;
    }

    setReauthInProgress(false);
  }, [restoringSession]);

  const fetchDocuments = useCallback(async () => {
    const logger = createLogger({ operation: 'documents list', target: '/api/graphql' });
    try {
      setIsListLoading(true);
      setListError(null);
      setRedirectingToSignIn(false);
      const correlationId = generateCorrelationId();
      const offset = (page - 1) * limit;

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
          variables: { pagination: { limit, offset } },
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

      const uniqueDocuments = Array.from(new Map(payload.nodes.map((doc) => [doc.id, doc])).values());
      setDocuments(uniqueDocuments);
      setDocumentsTotal(payload.totalCount);
      if (!restoringSession) {
        setReauthInProgress(false);
      }
    } catch (err) {
      const authHandled = handleAuthError(err, {
        onInteractionStart: () => {
          setRedirectingToSignIn(true);
          setListError('Требуется авторизация, перенаправляем на страницу входа…');
        },
      });
      if (authHandled || err instanceof SilentSignInError) {
        setReauthInProgress(false);
        return;
      }
      const messageText = err instanceof Error ? err.message : 'Не удалось загрузить список документов';
      logger.error('Не удалось загрузить список документов', { error: err });
      setListError(messageText);
    } finally {
      setIsListLoading(false);
      if (!restoringSession) {
        setReauthInProgress(false);
      }
    }
  }, [limit, page, restoringSession]);

  const pollDocument = useCallback(
    async (id: string) => {
      const logger = createLogger({ operation: 'document poll', target: '/api/graphql' });
      const errorCount = pollErrorAttempts.current.get(id) ?? 0;
      if (errorCount >= MAX_POLL_ERRORS) {
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

        pollErrorAttempts.current.delete(id);
        setDocuments((prev) => prev.map((item) => (item.id === doc.id ? { ...item, ...doc } : item)));
      } catch (err) {
        const authHandled = handleAuthError(err, {
          onInteractionStart: () => {
            setRedirectingToSignIn(true);
            logger.warn('Polling остановлен: требуется интерактивный вход', {
              correlationId,
              documentId: id,
            });
          },
        });
        if (authHandled || err instanceof SilentSignInError) {
          return;
        }
        const nextAttempts = errorCount + 1;
        pollErrorAttempts.current.set(id, nextAttempts);
        if (nextAttempts >= MAX_POLL_ERRORS) {
          logger.error('Polling остановлен после максимального количества неудач', {
            correlationId,
            attempts: nextAttempts,
            documentId: id,
          });
          return;
        }
        logger.error('Ошибка при polling документа', {
          correlationId,
          documentId: id,
          error: err,
          attempts: nextAttempts,
        });
      }
    },
    [],
  );

  useEffect(() => {
    fetchDocuments();
  }, [fetchDocuments]);

  useEffect(() => {
    stopPolling();

    if (redirectingToSignIn) {
      return undefined;
    }

    const ids = new Set(
      documents
        .filter((doc) => POLLABLE_STATUSES.includes(doc.status))
        .map((doc) => doc.id),
    );

    if (ids.size === 0) return undefined;

    pollingIntervalRef.current = setInterval(() => {
      ids.forEach((id) => pollDocument(id));
    }, 5000);

    return stopPolling;
  }, [documents, pollDocument, redirectingToSignIn, stopPolling]);

  useEffect(() => stopPolling, [stopPolling]);

  useEffect(() => {
    if (typeof documentsTotal !== 'number' || documentsTotal === 0) {
      return;
    }
    const maxPage = Math.max(1, Math.ceil(documentsTotal / limit));
    if (page > maxPage) {
      setPage(maxPage);
    }
  }, [documentsTotal, limit, page]);

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
          {showRestoringOverlay && (
            <div className="mb-4 flex items-center gap-3 rounded-lg border border-indigo-200 bg-indigo-50 px-3 py-2 text-sm text-indigo-800">
              <span
                className="inline-flex h-4 w-4 animate-spin rounded-full border-2 border-indigo-600 border-r-transparent"
                aria-hidden
              />
              <span>Восстанавливаем сессию…</span>
            </div>
          )}
          {redirectingToSignIn && (
            <div className="mb-4 flex items-center gap-3 rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-800">
              <span
                className="inline-flex h-4 w-4 animate-spin rounded-full border-2 border-amber-600 border-r-transparent"
                aria-hidden
              />
              <span>Требуется вход. Перенаправляем на страницу авторизации…</span>
            </div>
          )}
          <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
            <h2 className="text-lg font-semibold text-slate-900">Документы</h2>
            <button
              type="button"
              onClick={fetchDocuments}
              disabled={isListLoading}
              className="inline-flex items-center justify-center rounded-lg bg-indigo-600 px-3 py-2 text-sm font-medium text-white shadow-sm transition hover:bg-indigo-500 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-indigo-400 disabled:cursor-not-allowed disabled:opacity-60"
            >
              Обновить
            </button>
          </div>

          <div className="mt-4 space-y-4">
            <div className="hidden md:block">
              <DocumentsTable
                documents={documents}
                error={listError}
                isLoading={isListLoading}
                onRetry={fetchDocuments}
              />
            </div>

            <div className="grid gap-3 md:hidden">
              {isListLoading && documents.length === 0 && <DocumentsSkeleton />}

              {listError && (
                <div className="rounded-xl border border-red-200 bg-red-50 p-4 shadow-sm">
                  <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                    <p className="text-sm text-red-800">{listError}</p>
                    <button
                      type="button"
                      onClick={fetchDocuments}
                      className="w-full rounded-lg bg-indigo-600 px-3 py-2 text-sm font-medium text-white shadow-sm transition hover:bg-indigo-500 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-indigo-400 sm:w-auto"
                    >
                      Повторить
                    </button>
                  </div>
                </div>
              )}

              {!listError && !isListLoading && documents.length === 0 && (
                <p className="text-sm text-slate-600">Документы пока не загружались.</p>
              )}

              {documents.map((doc) => (
                <DocumentCard key={doc.id} document={doc} />
              ))}
            </div>
          </div>

          <Pagination
            documents={documents}
            documentsTotal={documentsTotal}
            isLoading={isListLoading}
            limit={limit}
            page={page}
            onNext={() => setPage((prev) => prev + 1)}
            onPrev={() => setPage((prev) => Math.max(1, prev - 1))}
            onLimitChange={(value) => {
              setPage(1);
              setLimit(value);
            }}
            onRefresh={fetchDocuments}
          />
        </div>
      </div>
    </main>
  );
}

type PaginationProps = {
  documents: KnowledgeDocument[];
  documentsTotal: number | null;
  isLoading: boolean;
  limit: number;
  page: number;
  onNext: () => void;
  onPrev: () => void;
  onLimitChange: (value: number) => void;
  onRefresh: () => void;
};

function Pagination({
  documents,
  documentsTotal,
  isLoading,
  limit,
  page,
  onNext,
  onPrev,
  onLimitChange,
  onRefresh,
}: PaginationProps) {
  const totalPages = typeof documentsTotal === 'number' ? Math.max(1, Math.ceil(documentsTotal / limit)) : null;
  const canGoPrev = page > 1 && !isLoading;
  const canGoNext =
    !isLoading && (typeof totalPages === 'number' ? page < totalPages : documents.length > 0 && documents.length >= limit);
  const offset = (page - 1) * limit;
  const startIndex = documents.length > 0 ? offset + 1 : 0;
  const endIndex = documents.length > 0 ? offset + documents.length : 0;

  return (
    <div className="mt-4 space-y-3 border-t border-slate-100 pt-4">
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        {typeof documentsTotal === 'number' ? (
          <p className="text-xs text-slate-600">
            Показаны {startIndex}–{endIndex} из {documentsTotal} (страница {page}
            {typeof totalPages === 'number' ? ` из ${totalPages}` : ''})
          </p>
        ) : (
          <p className="text-xs text-slate-600">Показано {documents.length}</p>
        )}

        <div className="flex flex-wrap items-center gap-3">
          <label className="flex items-center gap-2 text-xs text-slate-600" htmlFor="documents-limit">
            На странице
            <select
              id="documents-limit"
              className="rounded-md border border-slate-200 bg-white px-2 py-1 text-xs text-slate-900 shadow-sm focus:outline-none focus:ring-2 focus:ring-indigo-500 disabled:cursor-not-allowed disabled:opacity-60"
              value={limit}
              onChange={(event) => onLimitChange(Number(event.target.value))}
              disabled={isLoading}
            >
              {[10, 20, 50].map((value) => (
                <option key={value} value={value}>
                  {value}
                </option>
              ))}
            </select>
          </label>

          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={onPrev}
              disabled={!canGoPrev}
              className="inline-flex items-center justify-center rounded-lg border border-slate-200 bg-white px-3 py-2 text-xs font-medium text-slate-700 shadow-sm transition hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-60"
            >
              Назад
            </button>
            <span className="text-xs text-slate-600">Страница {page}</span>
            <button
              type="button"
              onClick={onNext}
              disabled={!canGoNext}
              className="inline-flex items-center justify-center rounded-lg bg-indigo-600 px-3 py-2 text-xs font-medium text-white shadow-sm transition hover:bg-indigo-500 disabled:cursor-not-allowed disabled:opacity-60"
            >
              Далее
            </button>
          </div>

          <button
            type="button"
            onClick={onRefresh}
            disabled={isLoading}
            className="inline-flex items-center justify-center rounded-lg border border-slate-200 bg-white px-3 py-2 text-xs font-medium text-slate-700 shadow-sm transition hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-60"
          >
            Обновить
          </button>
        </div>
      </div>

      {typeof documentsTotal === 'number' && (
        <p className="text-xs text-slate-500">Всего документов: {documentsTotal}</p>
      )}
    </div>
  );
}

function DocumentsTable({
  documents,
  isLoading,
  error,
  onRetry,
}: {
  documents: KnowledgeDocument[];
  isLoading: boolean;
  error: string | null;
  onRetry: () => void;
}) {
  const shouldShowSkeleton = isLoading && documents.length === 0;
  const shouldShowNoData = !isLoading && !error && documents.length === 0;

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
        {shouldShowSkeleton ? (
          Array.from({ length: 3 }).map((_, index) => (
            <TableRow key={`skeleton-${index}`}>
              <TableCell>
                <div className="space-y-2">
                  <div className="h-4 w-3/4 animate-pulse rounded bg-slate-100" />
                  <div className="h-3 w-1/2 animate-pulse rounded bg-slate-100" />
                  <div className="h-3 w-1/3 animate-pulse rounded bg-slate-100" />
                </div>
              </TableCell>
              <TableCell>
                <div className="h-6 w-24 animate-pulse rounded bg-slate-100" />
              </TableCell>
              <TableCell>
                <div className="h-4 w-28 animate-pulse rounded bg-slate-100" />
              </TableCell>
              <TableCell className="text-right">
                <div className="ml-auto h-9 w-20 animate-pulse rounded bg-slate-100" />
              </TableCell>
            </TableRow>
          ))
        ) : (
          <>
            {error && (
              <TableRow>
                <TableCell colSpan={4} className="py-4">
                  <div className="flex flex-col items-start justify-between gap-3 sm:flex-row sm:items-center">
                    <p className="text-sm text-red-700">{error}</p>
                    <button
                      type="button"
                      onClick={onRetry}
                      className="inline-flex items-center justify-center rounded-lg bg-indigo-600 px-3 py-2 text-sm font-medium text-white shadow-sm transition hover:bg-indigo-500 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-indigo-400"
                    >
                      Повторить
                    </button>
                  </div>
                </TableCell>
              </TableRow>
            )}

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

            {shouldShowNoData && (
              <TableRow>
                <TableCell colSpan={4} className="py-6 text-center text-sm text-slate-600">
                  Нет данных
                </TableCell>
              </TableRow>
            )}
          </>
        )}
      </TableBody>
    </Table>
  );
}

function DocumentsSkeleton() {
  return (
    <>
      {Array.from({ length: 3 }).map((_, index) => (
        <div
          key={`document-skeleton-${index}`}
          className="h-36 rounded-xl border border-slate-200 bg-white p-4 shadow-sm"
        >
          <div className="flex h-full flex-col gap-3">
            <div className="space-y-2">
              <div className="h-4 w-1/2 animate-pulse rounded bg-slate-100" />
              <div className="h-3 w-1/3 animate-pulse rounded bg-slate-100" />
              <div className="h-3 w-1/4 animate-pulse rounded bg-slate-100" />
            </div>
            <div className="flex flex-wrap items-center gap-3">
              <div className="h-6 w-24 animate-pulse rounded bg-slate-100" />
              <div className="h-4 w-28 animate-pulse rounded bg-slate-100" />
              <div className="ml-auto h-9 w-24 animate-pulse rounded bg-slate-100" />
            </div>
          </div>
        </div>
      ))}
    </>
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

type TableCellProps = React.TdHTMLAttributes<HTMLTableCellElement> & {
  children: React.ReactNode;
  className?: string;
};

function TableCell({ children, className, ...props }: TableCellProps) {
  return (
    <td {...props} className={cn('p-4 align-top text-sm text-slate-900', className)}>
      {children}
    </td>
  );
}
