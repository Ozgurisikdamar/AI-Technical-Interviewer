import type { Metadata } from 'next';
import { SignIn } from '@/features/staff/SignIn';
import { getMessages } from '@/i18n';

export const metadata: Metadata = {
  title: { absolute: getMessages().staff.meta.signIn },
};

export default function StaffSignInPage() {
  return <SignIn />;
}
