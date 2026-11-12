/**
 * What an admin reads under a message that did not send.
 *
 * The stored error is whatever whatsmeow returned, and it was shown verbatim:
 * "failed to get device list: failed to send usync query: info query timed
 * out" says nothing to somebody who only needs to know whether to press send
 * again. The cases below are the ones the messages table actually holds, most
 * common first; the raw text stays available as a tooltip for whoever has to
 * trace it.
 */
const RULES: Array<[RegExp, string]> = [
  // The device was unlinked or the session refused.
  [
    /server returned error 401/i,
    'Nomor ini sedang tidak terhubung ke WhatsApp. Hubungkan ulang nomornya, lalu kirim lagi.',
  ],
  // Rate limited by WhatsApp.
  [
    /server returned error 420/i,
    'WhatsApp sedang membatasi pengiriman dari nomor ini. Tunggu beberapa saat sebelum mengirim lagi.',
  ],
  [/server returned error 403/i, 'WhatsApp menolak pesan ke chat ini.'],
  [/not participating in that group/i, 'Nomor ini sudah tidak menjadi anggota grup ini.'],
  [/no LID found/i, 'Nomor tujuan tidak ditemukan di WhatsApp.'],
  // Timeouts and dropped connections, on either side: nothing is wrong with
  // the message, and the same send a minute later goes through.
  [
    /deadline exceeded|timed out|bad connection|status code 5\d\d|failed to load sender key|context canceled/i,
    'Koneksi ke WhatsApp sedang terganggu saat pesan dikirim. Kirim ulang pesannya.',
  ],
];

const FALLBACK = 'Pesan tidak terkirim. Coba kirim ulang.';

// English library errors, as opposed to sentences salesan already wrote for
// this screen, which pass through unchanged.
const TECHNICAL = /\b(failed to|error|server returned|context|cipher|query)\b/i;

export function sendErrorMessage(raw: string): string {
  for (const [pattern, message] of RULES) {
    if (pattern.test(raw)) return message;
  }
  return TECHNICAL.test(raw) ? FALLBACK : raw;
}
