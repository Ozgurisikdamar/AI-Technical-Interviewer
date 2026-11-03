'use client';

import { useEffect, useId, useRef, type ReactNode } from 'react';
import styles from './ConfirmDialog.module.css';
import ui from './ui.module.css';

interface ConfirmDialogProps {
  open: boolean;
  title: string;
  children: ReactNode;
  confirmLabel: string;
  cancelLabel: string;
  onConfirm: () => void;
  onCancel: () => void;
  confirmTestId?: string;
  busy?: boolean;
}

/**
 * Modal confirmation built on the native <dialog>: focus is trapped while open, Escape cancels,
 * the safe choice (cancel) comes first and gets initial focus, and focus returns to the trigger
 * when the dialog closes.
 */
export function ConfirmDialog({
  open,
  title,
  children,
  confirmLabel,
  cancelLabel,
  onConfirm,
  onCancel,
  confirmTestId,
  busy = false,
}: ConfirmDialogProps) {
  const ref = useRef<HTMLDialogElement>(null);
  const titleId = useId();
  const bodyId = useId();

  useEffect(() => {
    const dialog = ref.current;
    if (!dialog) return;
    if (open && !dialog.open) dialog.showModal();
    if (!open && dialog.open) dialog.close();
  }, [open]);

  return (
    <dialog
      ref={ref}
      className={styles.dialog}
      aria-labelledby={titleId}
      aria-describedby={bodyId}
      onCancel={(event) => {
        event.preventDefault();
        if (!busy) onCancel();
      }}
    >
      <h2 id={titleId} className={styles.title}>
        {title}
      </h2>
      <div id={bodyId}>{children}</div>
      <div className={ui.actions}>
        <button type="button" className={`${ui.button} ${ui.secondary}`} onClick={onCancel} disabled={busy}>
          {cancelLabel}
        </button>
        <button
          type="button"
          className={`${ui.button} ${ui.dangerSolid}`}
          onClick={onConfirm}
          disabled={busy}
          data-testid={confirmTestId}
        >
          {confirmLabel}
        </button>
      </div>
    </dialog>
  );
}
