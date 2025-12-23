'use client';

import React, { useEffect, useMemo, useState } from 'react';
import { signIn } from 'next-auth/react';
import { generateCorrelationId } from '@/lib/correlation';
import { SilentSignInError, fetchWithAuth } from '@/lib/http-client';

type UploadStatus = 'idle' | 'uploading' | 'done' | 'error';

type KnowledgeDocumentStatus =
  | 'UPLOADED'
  | 'STORED_ORIGINAL'
  | 'CONVERTED_TO_MARKDOWN'
  | 'PUBLISHED_TO_GITHUB'
  | 'FAILED';

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
        status: KnowledgeDocumentStatus;
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
  const [manualLoginRequired, setManualLoginRequired] = useState(false);

  const repository = process.env.NEXT_PUBLIC_DEFAULT_REPOSITORY ?? 'docs';
  const branch = process.env.NEXT_PUBLIC_DEFAULT_BRANCH ?? 'main';

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
    setManualLoginRequired(false);

    if (!file) {
      setMessage('Выберите файл');
      return;
    }
    if (!resolvePath.path) {
      setValidationError(resolvePath.error ?? 'Заполните необходимые поля для пути');
      return;
    }

    try {
      setStatus('uploading');

      const correlationId = generateCorrelationId();

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

      const gqlResp = await fetchWithAuth('/api/graphql', {
        method: 'POST',
        cache: 'no-store',
        headers: {
          'Content-Type': 'application/json',
          'X-Correlation-ID': correlationId,
        },
        body: JSON.stringify({ query: mutation, variables }),
      });

      if (!gqlResp.ok) {
        throw new Error(`Ошибка gateway: ${gqlResp.status} ${gqlResp.statusText}`);
      }

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

      const putResp = await fetch(uploadUrl, {
        method: 'PUT',
        headers: {
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
      if (err instanceof SilentSignInError) {
        setManualLoginRequired(err.interactionRequired);
        const friendlyMessage = err.interactionRequired
          ? 'Сессия истекла. Войдите вручную, чтобы продолжить загрузку.'
          : err.message;
        setStatus('error');
        setMessage(friendlyMessage);
        return;
      }
      setStatus('error');
      const errorMessage = err instanceof Error ? err.message : 'Неизвестная ошибка';
      setMessage(errorMessage);
    }
  };

  return (
    <main className="px-4 pb-12">
      <div className="mx-auto max-w-4xl space-y-6">
        <div className="rounded-2xl border border-slate-200 bg-white p-6 shadow-sm">
          <div className="flex flex-col gap-2">
            <h1 className="text-2xl font-semibold">Загрузка DOC/DOCX/PDF</h1>
            <p className="text-sm text-slate-600">
              Укажите категорию и путь для будущего Markdown-файла, затем загрузите исходный документ.
            </p>
          </div>

          <form onSubmit={handleSubmit} className="mt-6 space-y-4">
            <div>
              <label className="mb-1 block text-sm text-slate-600">Файл</label>
              <input
                type="file"
                accept=".doc,.docx,.pdf"
                onChange={handleFileChange}
                className="block w-full cursor-pointer rounded-lg border border-slate-300 bg-white text-sm text-slate-900 file:mr-4 file:rounded-lg file:border-0 file:bg-indigo-600 file:px-4 file:py-2 file:text-sm file:font-semibold file:text-white hover:file:bg-indigo-500 disabled:file:cursor-not-allowed disabled:file:bg-indigo-300 disabled:file:text-white focus:outline-none focus:ring-2 focus:ring-indigo-500"
              />
              {file && (
                <p className="mt-1 text-xs text-slate-500">
                  {file.name} ({file.size} байт)
                </p>
              )}
            </div>

            <div className="grid grid-cols-1 gap-4">
              <div className="space-y-3">
                <div>
                  <label className="mb-1 block text-sm text-slate-600">Категория документа</label>
                  <select
                    value={category}
                    onChange={(e) => setCategory(e.target.value as Category)}
                    className="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 outline-none focus:ring-2 focus:ring-indigo-500"
                  >
                    <option value="plenary">Пленарное заседание</option>
                    <option value="bibliography">Библиография</option>
                  </select>
                </div>

                {category === 'plenary' && (
                  <div className="space-y-3 rounded-lg border border-slate-200 bg-slate-50 p-3">
                    <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
                      <div>
                        <label className="mb-1 block text-sm text-slate-600">Номер заседания</label>
                        <input
                          type="text"
                          value={plenaryFields.sessionNumber}
                          onChange={(e) => updatePlenaryFields({ sessionNumber: e.target.value })}
                          placeholder="например: 5"
                          className="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 outline-none focus:ring-2 focus:ring-indigo-500"
                        />
                      </div>
                      <div>
                        <label className="mb-1 block text-sm text-slate-600">Тип документа</label>
                        <select
                          value={plenaryFields.documentType}
                          onChange={(e) =>
                            updatePlenaryFields({ documentType: e.target.value as PlenaryDocumentType })
                          }
                          className="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 outline-none focus:ring-2 focus:ring-indigo-500"
                        >
                          <option value="transcript">Стенограмма</option>
                          <option value="voting">Протокол голосования</option>
                        </select>
                      </div>
                    </div>

                    {plenaryFields.documentType === 'transcript' ? (
                      <div>
                        <label className="mb-1 block text-sm text-slate-600">Номер стенограммы</label>
                        <input
                          type="text"
                          value={plenaryFields.transcriptNumber}
                          onChange={(e) => updatePlenaryFields({ transcriptNumber: e.target.value })}
                          placeholder="например: 3"
                          className="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 outline-none focus:ring-2 focus:ring-indigo-500"
                        />
                      </div>
                    ) : (
                      <div>
                        <label className="mb-1 block text-sm text-slate-600">Номер вопроса голосования</label>
                        <input
                          type="text"
                          value={plenaryFields.votingQuestionNumber}
                          onChange={(e) => updatePlenaryFields({ votingQuestionNumber: e.target.value })}
                          placeholder="например: 12"
                          className="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 outline-none focus:ring-2 focus:ring-indigo-500"
                        />
                      </div>
                    )}
                  </div>
                )}

                {category === 'bibliography' && (
                  <div className="space-y-3 rounded-lg border border-slate-200 bg-slate-50 p-3">
                    <div>
                      <label className="mb-1 block text-sm text-slate-600">Подкатегория</label>
                      <select
                        value={bibliographyFields.type}
                        onChange={(e) => updateBibliographyFields({ type: e.target.value as BibliographyCategory })}
                        className="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 outline-none focus:ring-2 focus:ring-indigo-500"
                      >
                        <option value="deputies">Депутаты</option>
                      </select>
                    </div>
                    <div>
                      <label className="mb-1 block text-sm text-slate-600">Slug / идентификатор</label>
                      <input
                        type="text"
                        value={bibliographyFields.slug}
                        onChange={(e) => updateBibliographyFields({ slug: e.target.value })}
                        placeholder="например: ivanov-ivan"
                        className="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 outline-none focus:ring-2 focus:ring-indigo-500"
                      />
                      <p className="mt-1 text-xs text-slate-500">
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
              className="mt-2 inline-flex items-center justify-center rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-500 disabled:cursor-not-allowed disabled:opacity-60"
            >
              {status === 'uploading' ? 'Загружаю…' : 'Загрузить документ'}
            </button>
          </form>

          {message && (
            <div
              className={`mt-4 rounded-lg border px-3 py-2 text-sm ${
                status === 'error'
                  ? 'border-red-200 bg-red-50 text-red-800'
                  : 'border-emerald-200 bg-emerald-50 text-emerald-800'
              }`}
            >
              {message}
            </div>
          )}

          {manualLoginRequired && (
            <div className="mt-3 flex items-center gap-3">
              <button
                type="button"
                onClick={() => signIn('authentik', { callbackUrl: window.location.href })}
                className="inline-flex items-center justify-center rounded-lg border border-indigo-200 bg-indigo-50 px-3 py-2 text-sm font-medium text-indigo-700 transition hover:bg-indigo-100"
              >
                Войти вручную
              </button>
              <p className="text-xs text-slate-600">
                Silent sign-in потребовал взаимодействие. Нажмите, чтобы открыть форму входа.
              </p>
            </div>
          )}

          {validationError && (
            <div className="mt-4 rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-800">
              {validationError}
            </div>
          )}

          {resolvePath.path && (
            <div className="mt-4 rounded-lg border border-slate-200 bg-slate-50 px-3 py-2">
              <p className="text-xs uppercase text-slate-500">Предпросмотр пути</p>
              <p className="font-mono text-sm text-slate-900">{resolvePath.path}</p>
            </div>
          )}

          {documentId && (
            <p className="mt-2 text-xs text-slate-500">
              ID документа: <span className="font-mono">{documentId}</span>
            </p>
          )}
        </div>
      </div>
    </main>
  );
}
