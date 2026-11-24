// A payment in the chat it was paid from (docs/spec/chat-pay-people.md,
// "After"; build-plan-gaps H7; web/src/pages/PayDiary.tsx): its diary
// line, "Paid Chidi Okeke ₦45,000 (delivery) · GTBank ••6789", with how the
// payment stands now ("Received by the bank", or "Failed: refunded to your
// balance" with Try again); a batch's one line with how its rows stand; a
// receipt sent into the chat, and the card a staff member sees when the
// business paid them.

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../app/auth.dart';
import '../kit/kit.dart';
import '../kit/look.dart';
import '../kit/sheet.dart' show showTkSheet;
import '../theme/look.dart';
import 'cards.dart' show DiaryLine;
import 'pay_many.dart' show BatchSheet, batchStatus;
import 'pay_sheet.dart' show PaymentSheet, payDiaryState;
import 'payments_page.dart' show openPayment;

/// A payment's (or a batch's) diary line, with how it stands now. Tapping
/// it opens the payment; one that did not reach them has Try again.
class PayDiaryLine extends StatefulWidget {
  const PayDiaryLine({super.key, required this.auth, required this.words, required this.when, this.payment, this.batch});
  final Auth auth;
  final String words;
  final String when;
  final String? payment;
  final String? batch;

  @override
  State<PayDiaryLine> createState() => _PayDiaryLineState();
}

class _PayDiaryLineState extends State<PayDiaryLine> {
  Payment? _p;
  PaymentBatch? _b;
  Timer? _again;

  @override
  void initState() {
    super.initState();
    unawaited(_load());
  }

  @override
  void dispose() {
    _again?.cancel();
    super.dispose();
  }

  Future<void> _load() async {
    try {
      if (widget.payment != null) {
        final p = (await widget.auth.api.getPayment(id: widget.payment!)).payment;
        if (!mounted) return;
        setState(() => _p = p);
        // Still on its way: asked again in a while.
        if (const {'sending', 'unknown', 'waiting_ok'}.contains(p.status)) _again = Timer(const Duration(seconds: 15), () => unawaited(_load()));
      } else if (widget.batch != null) {
        final b = (await widget.auth.api.getPaymentBatch(id: widget.batch!)).batch;
        if (mounted) setState(() => _b = b);
      }
    } on Object {
      // The line stands as it was written.
    }
  }

  Future<void> _open() async {
    if (widget.payment != null) {
      await openPayment(context, widget.auth, widget.payment!);
    } else if (_b != null) {
      await showTkSheet<void>(context, (_) => BatchSheet(auth: widget.auth, batch: _b!));
    }
    if (mounted) unawaited(_load());
  }

  @override
  Widget build(BuildContext context) {
    final p = _p;
    final b = _b;
    final state = p == null ? null : payDiaryState(p.status);
    return DiaryLine(
      words: widget.words,
      sub: state != null ? '· ${state.words}' : (b != null ? '· ${batchStatus(b).words}' : null),
      when: widget.when,
      onTap: p != null || b != null ? _open : null,
      more: state != null && state.again
          ? Padding(
              padding: const EdgeInsets.only(top: 6),
              child: TkSmallButton(
                label: 'Try again',
                primary: true,
                onPressed: () async {
                  try {
                    final again = (await widget.auth.api.payAgain(id: p!.id)).payment;
                    if (!context.mounted) return;
                    await showTkSheet<Payment>(context, (_) => PaymentSheet(auth: widget.auth, payment: again, fresh: true));
                  } on Object {
                    if (context.mounted) unawaited(openPayment(context, widget.auth, p!.id));
                  }
                },
              ),
            )
          : null,
    );
  }
}

/// A receipt sent into the chat, or a payment to this person ("paid you"):
/// a small card with its words; tapping a receipt opens its payment.
class PayChatCard extends StatelessWidget {
  const PayChatCard({super.key, required this.auth, required this.kind, required this.text, this.payment});
  final Auth auth;
  final String kind;
  final String text;
  final String? payment;

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    final receipt = kind == 'receipt';
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 6),
      child: TkCallout(
        leading: TkSquare(receipt ? 'b-check' : 'b-bank', color: look.teal),
        title: receipt ? 'Receipt' : 'Paid to you',
        sub: text,
        subLines: 4,
        color: look.teal,
        button: receipt && payment != null ? 'Open' : null,
        semantics: receipt ? 'Open the payment' : null,
        onPressed: receipt && payment != null ? () => unawaited(openPayment(context, auth, payment!)) : null,
      ),
    );
  }
}
