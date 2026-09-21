import { redirect } from 'next/navigation';

/** The account grid is the app's landing screen (reference screen 1). */
export default function RootPage() {
  redirect('/accounts');
}
