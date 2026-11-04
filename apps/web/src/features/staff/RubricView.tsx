'use client';

import ui from '@/components/ui.module.css';
import { useMessages } from '@/i18n/client';
import { DIMENSIONS, type Rubric } from '@/lib/api/staffTypes';
import { weightPercent } from '@/lib/staff/weighted';
import styles from './staff.module.css';

/** The rubric as a reviewer sees it: weights, criteria by dimension, and https references. */
export function RubricView({ rubric, headingId }: { rubric: Rubric; headingId: string }) {
  const t = useMessages();
  const references = new Map(rubric.references.map((r) => [r.reference_id, r]));
  return (
    <section aria-labelledby={headingId} className={styles.section} data-testid="rubric">
      <h2 id={headingId}>{t.staff.task.rubric}</h2>
      <p>
        <strong>{rubric.title}</strong>{' '}
        <span className={`${styles.meta} ${styles.mono}`}>
          {rubric.rubric_id}@{rubric.version}
        </span>
      </p>
      {rubric.synthetic ? <p className={ui.warning}>{t.staff.task.rubricSynthetic}</p> : null}
      {rubric.status === 'draft' ? <p className={styles.meta}>{t.staff.task.rubricDraft}</p> : null}
      {DIMENSIONS.map((dimension) => {
        const criteria = rubric.criteria.filter((c) => c.dimension === dimension);
        return (
          <div key={dimension} className={styles.dimension}>
            <h3>
              {t.staff.dimensions[dimension]}{' '}
              <span className={styles.weight}>{t.staff.task.weight(weightPercent(rubric.weights[dimension]))}</span>
            </h3>
            {criteria.length > 0 ? (
              <ul className={styles.criteria}>
                {criteria.map((criterion) => (
                  <li key={criterion.criterion_id}>
                    {criterion.description}
                    {criterion.reference_ids.length > 0 ? (
                      <span className={styles.meta}>
                        {' '}
                        ({criterion.reference_ids.map((id) => references.get(id)?.title ?? id).join('; ')})
                      </span>
                    ) : null}
                  </li>
                ))}
              </ul>
            ) : null}
          </div>
        );
      })}
      {rubric.references.length > 0 ? (
        <>
          <h3>{t.staff.task.references}</h3>
          <dl className={styles.references}>
            {rubric.references.map((reference) => (
              <div key={reference.reference_id}>
                <dt>
                  {reference.url !== null ? (
                    <a href={reference.url} target="_blank" rel="noopener noreferrer">
                      {reference.title}
                      <span className="visually-hidden"> {t.staff.task.opensInNewTab}</span>
                    </a>
                  ) : (
                    reference.title
                  )}
                </dt>
                <dd>{reference.summary}</dd>
              </div>
            ))}
          </dl>
        </>
      ) : null}
    </section>
  );
}
