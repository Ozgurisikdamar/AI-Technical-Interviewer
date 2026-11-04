import type { Metadata } from 'next';
import { InvitationExchange } from '@/features/invitation/InvitationExchange';
import { getMessages } from '@/i18n';

export const metadata: Metadata = {
  title: getMessages().meta.invitationTitle,
};

/** `/i#token=…`: the token stays in the fragment; the server never sees it in this request. */
export default function InvitationPage() {
  return <InvitationExchange />;
}
