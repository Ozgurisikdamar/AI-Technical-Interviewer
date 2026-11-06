import type { Metadata } from 'next';
import { SignIn } from '@/features/staff/SignIn';
import { getMessages } from '@/i18n';
import { requestLocale } from '@/i18n/server';

export async function generateMetadata(): Promise<Metadata> {
  return { title: { absolute: getMessages(await requestLocale()).staff.meta.signIn } };
}

export default async function StaffSignInPage({ searchParams }: { searchParams: Promise<Record<string, string | string[] | undefined>> }) {
  // Where to go after signing in; SignIn follows it only to a console page the role may open.
  const { next } = await searchParams;
  return <SignIn next={typeof next === 'string' ? next : undefined} />;
}
