'use client';

import ui from './ui.module.css';

interface ErrorPageProps {
  messages: { readonly heading: string; readonly body: string; readonly retry: string };
  retry: () => void;
}

/** The body of an error boundary. Never renders the error's message: it may contain request data. */
export function ErrorPage({ messages, retry }: ErrorPageProps) {
  return (
    <div className={ui.page} role="alert">
      <h1>{messages.heading}</h1>
      <p>{messages.body}</p>
      <div className={ui.actions}>
        <button type="button" className={`${ui.button} ${ui.primary}`} onClick={() => retry()}>
          {messages.retry}
        </button>
      </div>
    </div>
  );
}
