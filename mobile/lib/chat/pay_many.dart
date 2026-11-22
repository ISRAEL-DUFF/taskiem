// Pay several people (P3; docs/spec/chat-pay-people.md "Pay several
// people"): the Pay sheet with a list. Rows come from saved accounts,
// riders (what each is due, GET /v1/riders/pay), staff with an account
// linked (GET /v1/payments/staff) or a pasted list (read by
// POST /v1/payment-batches/read, a line that cannot be read kept to fix),
// or a photo of a list, read on the phone and sent as words (`from:
// "photo"`) through the same flow;
// each row is who, how much and what for. Check prepares the batch (POST
// /v1/payment-batches): each row as one payment is, those failing a check
// held out in amber with why. One passkey sends the rows shown (money.pay.
// batch on batch:<id>, POST …/{id}/confirm with the row ids), and each row
// fills in as it goes; the failed ones can be paid again, an owner gives
// the OK for every row that waits for it with one passkey, and a batch
// from before can be made again (copy_of).

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../app/auth.dart';
import '../app/passkey.dart';
import '../kit/forms.dart' show errorWords, nairaText;
import '../kit/kit.dart';
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../kit/modal.dart' show showTkToast;
import '../kit/sheet.dart';
import '../theme/look.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'deal.dart' show dealKobo;
import 'groups.dart' show pickOneOf;
import 'pay_sheet.dart';
import 'photo_text.dart' show PhotoAI, PhotoAICallout, PhotoReadButton, PhotoReading, noWordsInPhoto, readPhotoFromPhone, readPhotoWithAI, readingPhoto;
import 'saved_fill.dart' show readSaved;
import 'words.dart' show CardTone, naira;

/// The most people a batch takes, as the server does.
const payManyMost = 100;

/// "1 person", "3 people".
String peopleWords(int n) => '$n ${n == 1 ? 'person' : 'people'}';

/// What a purpose key says, in the Pay sheet's words ("a delivery").
String purposeSaid(String key) => payPurposes.where((p) => p.$2 == key).firstOrNull?.$1 ?? '';

String _capital(String w) => w.isEmpty ? w : w[0].toUpperCase() + w.substring(1);

/// One person to pay in the list being filled: who (and how they are
/// paid: a saved account, a rider, or an account number and bank), how
/// much and what for.
class PayRow {
  PayRow({required this.who, this.sub = '', required this.ask, int? kobo, this.purpose = '', this.bank = '', this.number = ''})
    : amount = TextEditingController(text: kobo == null || kobo <= 0 ? '' : nairaText(kobo));

  /// "@bayo", "Musa", "Account ••6789".
  final String who;
  final String sub;

  /// How the server finds them: saved_id (and mine), payee, rider_id.
  final Map<String, dynamic> ask;
  final TextEditingController amount;

  /// The server's key (delivery); empty while not chosen.
  String purpose;

  /// For an account number not saved: the number, and the bank chosen.
  final String number;
  String bank;

  /// Why the server held it out, when it did.
  String? held;

  int? get kobo => dealKobo(amount.text.trim());

  bool get needsBank => number.isNotEmpty && bank.isEmpty;

  Map<String, dynamic> body() => {
    ...ask,
    if (number.isNotEmpty) ...{'account_number': number, 'bank_name': bank},
    'amount_kobo': kobo,
    'purpose': purpose,
  };
}

/// The riders due something, as rows to pay (to the bank kept for their
/// payouts, for deliveries); [noBank], those due with no bank account.
Future<({List<PayRow> rows, int noBank})> ridersDue(Auth auth) async {
  final r = (await auth.transport.send('GET', '/v1/riders/pay') as Map).cast<String, dynamic>();
  final rows = <PayRow>[];
  var noBank = 0;
  for (final l in (r['lines'] as List? ?? const []).whereType<Map<String, dynamic>>()) {
    final net = (l['net_kobo'] as num?)?.toInt() ?? 0;
    final id = l['rider_id'] as String? ?? '';
    if (net <= 0 || id.isEmpty) continue;
    final bank = l['bank'] as String? ?? '';
    if (bank.isEmpty) {
      noBank++;
      continue;
    }
    final n = (l['deliveries'] as num?)?.toInt() ?? 0;
    rows.add(
      PayRow(
        who: l['rider'] as String? ?? 'A rider',
        sub: ['$n ${n == 1 ? 'delivery' : 'deliveries'}', bank].join(' · '),
        ask: {'rider_id': id},
        kobo: net,
        purpose: 'delivery',
      ),
    );
  }
  return (rows: rows, noBank: noBank);
}

/// The staff with an account linked, as rows to pay (to that account, for
/// wages); [off], those without one, by name, with why.
Future<({List<PayRow> rows, List<(String, String)> off})> staffPayable(Auth auth) async {
  Map<String, dynamic> map(Object? v) => (v as Map?)?.cast<String, dynamic>() ?? const {};
  String str(Object? v) => v as String? ?? '';
  // The payers' staff list (GET /v1/payments/staff): each with where they
  // are paid, for anyone who may pay.
  final pages = [for (final x in (map(await auth.transport.send('GET', '/v1/payments/staff'))['staff'] as List? ?? const [])) map(x)];
  final rows = <PayRow>[];
  final off = <(String, String)>[];
  for (final x in pages) {
    final id = str(x['user_id']);
    if (id.isEmpty) continue;
    final name = str(x['name']).isNotEmpty ? str(x['name']) : (str(x['email']).isNotEmpty ? str(x['email']) : 'Someone');
    if (x['unread'] == true) {
      off.add((name, 'Their page could not be read just now'));
      continue;
    }
    final a = x['pay_account'] is Map ? map(x['pay_account']) : null;
    if (a == null) {
      off.add((name, 'No account linked: an owner links one on their page'));
      continue;
    }
    final bank = [str(a['bank_name']), if (str(a['last4']).isNotEmpty) '••${str(a['last4'])}'].where((w) => w.isNotEmpty).join(' ');
    rows.add(PayRow(who: name, sub: ['Staff', bank].where((w) => w.isNotEmpty).join(' · '), ask: {'staff_id': id}, purpose: 'wages'));
  }
  return (rows: rows, off: off);
}

/// Opens Pay several people over [context], with [rows] in it already;
/// the batch once it is sent, or null.
Future<PaymentBatch?> openPayMany(BuildContext context, Auth auth, {List<PayRow> rows = const [], String kind = 'list', String title = '', String? note}) =>
    showTkSheet<PaymentBatch>(context, (_) => PayManySheet(auth: auth, rows: rows, kind: kind, title: title, note: note));

/// Pay all riders: what each rider is due, as a batch to check and pay.
Future<PaymentBatch?> payAllRiders(BuildContext context, Auth auth) async {
  final ({List<PayRow> rows, int noBank}) due;
  try {
    due = await ridersDue(auth);
  } on Object catch (e) {
    if (context.mounted) showTkToast(context, errorWords(e), error: true);
    return null;
  }
  if (!context.mounted) return null;
  if (due.rows.isEmpty) {
    showTkToast(context, due.noBank > 0 ? 'No rider due pay has a bank account yet: add one in Pay riders.' : 'No rider is due anything just now.');
    return null;
  }
  return openPayMany(
    context,
    auth,
    rows: due.rows,
    kind: 'riders',
    title: 'Riders’ pay',
    note: due.noBank == 0 ? null : '${due.noBank} ${due.noBank == 1 ? 'rider is' : 'riders are'} due pay with no bank account: add one in Pay riders.',
  );
}

/// Pay several people: the rows, each who, how much and what for; Check
/// prepares them as a batch.
class PayManySheet extends StatefulWidget {
  const PayManySheet({super.key, required this.auth, this.rows = const [], this.kind = 'list', this.title = '', this.note, this.follow = true});
  final Auth auth;
  final List<PayRow> rows;

  /// list, riders or messages.
  final String kind;
  final String title;

  /// Said over the rows (riders left out, and why).
  final String? note;

  /// Read the batch again while rows are sent (not in a test's pictures).
  final bool follow;

  @override
  State<PayManySheet> createState() => _PayManySheetState();
}

class _PayManySheetState extends State<PayManySheet> {
  late final List<PayRow> _rows = [...widget.rows];
  final _paste = TextEditingController();
  bool _pasting = false;
  bool _reading = false;
  // The box holds words read from a photo: sent with `from: "photo"`.
  bool _fromPhoto = false;
  // A photo the phone read no line of, offered to be read with AI.
  PhotoReading? _unreadPhoto;
  bool _aiBusy = false;
  late final _ai = PhotoAI(widget.auth);
  bool _picking = false;
  bool _busy = false;
  // Lines of the pasted list that could not be read: kept in the box.
  List<ReadPaymentListResponseLinesItem> _unread = const [];
  PaymentBatch? _batch;
  String? _error;

  Auth get _auth => widget.auth;

  bool get _owner => (_auth.me?.roles as List?)?.contains('owner') ?? false;

  @override
  void initState() {
    super.initState();
    _paste.addListener(() => setState(() {
      if (_paste.text.trim().isEmpty) _fromPhoto = false;
    }));
    for (final r in _rows) {
      r.amount.addListener(_changed);
    }
  }

  @override
  void dispose() {
    _paste.dispose();
    for (final r in _rows) {
      r.amount.dispose();
    }
    super.dispose();
  }

  void _changed() => setState(() {});

  void _add(Iterable<PayRow> rows) {
    final room = payManyMost - _rows.length;
    final adding = rows.take(room < 0 ? 0 : room).toList();
    for (final r in adding) {
      r.amount.addListener(_changed);
    }
    setState(() {
      _rows.addAll(adding);
      _error = null;
    });
    if (adding.length < rows.length && mounted) showTkToast(context, 'Up to $payManyMost people a batch.');
  }

  void _remove(PayRow r) {
    setState(() => _rows.remove(r));
    r.amount.removeListener(_changed);
    // Disposed once the row's box is gone.
    WidgetsBinding.instance.addPostFrameCallback((_) => r.amount.dispose());
  }

  int get _total => _rows.fold(0, (n, r) => n + (r.kobo ?? 0));

  /// What is missing, for Check to wait on: '' for nothing in the list.
  String? get _problem {
    if (_rows.isEmpty) return '';
    if (_rows.any((r) => (r.kobo ?? 0) < 100)) return 'Say how much for each, like 45,000 or 45k.';
    if (_rows.any((r) => r.purpose.isEmpty)) return 'Say what each is for.';
    if (_rows.any((r) => r.needsBank)) return 'Choose the bank for each account number.';
    return null;
  }

  /// The business's saved accounts (an owner's own too), several picked.
  Future<void> _pickSaved() async {
    setState(() => _picking = true);
    try {
      final got = await readSaved(context, _auth, kind: 'bank_account', fits: (x) => x.kind == 'bank_account' && (x.owner != 'mine' || _owner));
      if (got == null || !mounted) return;
      final have = {
        for (final r in _rows)
          if (r.ask['saved_id'] != null) '${r.ask['saved_id']}',
      };
      final keys = await pickSeveral(
        context,
        title: 'Saved accounts',
        icon: 'b-bank',
        choices: [
          for (final x in got.all)
            if (!have.contains(x.id))
              (
                x.id,
                x.title.isEmpty ? '@${x.name}' : x.title,
                ['@${x.name}', x.hint, if (got.both) x.owner == 'mine' ? 'Yours' : 'The business’s'].where((w) => w.isNotEmpty).join(' · '),
              ),
        ],
      );
      if (keys == null || !mounted) return;
      _add([
        for (final x in got.all)
          if (keys.contains(x.id))
            PayRow(
              who: x.title.isEmpty ? '@${x.name}' : x.title,
              sub: ['@${x.name}', x.hint].where((w) => w.isNotEmpty).join(' · '),
              ask: {'saved_id': x.id, if (x.owner == 'mine') 'mine': true},
            ),
      ]);
    } finally {
      if (mounted) setState(() => _picking = false);
    }
  }

  /// The riders due pay, those not in the list yet.
  Future<void> _pickRiders() async {
    setState(() => _picking = true);
    try {
      final due = await ridersDue(_auth);
      if (!mounted) return;
      final have = {
        for (final r in _rows)
          if (r.ask['rider_id'] != null) '${r.ask['rider_id']}',
      };
      final rows = due.rows.where((r) => !have.contains('${r.ask['rider_id']}')).toList();
      if (rows.isEmpty) {
        showTkToast(context, due.noBank > 0 ? 'No other rider due pay has a bank account.' : 'No other rider is due anything.');
        return;
      }
      _add(rows);
    } on Object catch (e) {
      if (mounted) showTkToast(context, errorWords(e), error: true);
    } finally {
      if (mounted) setState(() => _picking = false);
    }
  }

  /// Staff with an account linked, several picked; those without one
  /// shown as not payable, with why.
  Future<void> _pickStaff() async {
    setState(() => _picking = true);
    try {
      final got = await staffPayable(_auth);
      if (!mounted) return;
      final have = {
        for (final r in _rows)
          if (r.ask['staff_id'] != null) '${r.ask['staff_id']}',
      };
      final rows = got.rows.where((r) => !have.contains('${r.ask['staff_id']}')).toList();
      if (rows.isEmpty && got.off.isEmpty) {
        showTkToast(context, got.rows.isEmpty ? 'No staff to pay yet.' : 'All the staff with an account are in the list already.');
        return;
      }
      final keys = await pickSeveral(
        context,
        title: 'Staff',
        icon: 'b-people',
        choices: [for (final r in rows) ('${r.ask['staff_id']}', r.who, r.sub)],
        off: got.off,
        empty: got.rows.isEmpty ? 'No one has an account linked yet.' : null,
      );
      if (keys == null || !mounted) return;
      _add([
        for (final r in rows)
          if (keys.contains('${r.ask['staff_id']}')) r,
      ]);
    } on Object catch (e) {
      if (mounted) showTkToast(context, errorWords(e), error: true);
    } finally {
      if (mounted) setState(() => _picking = false);
    }
  }

  /// The pasted list read: each line read becomes a row; those that
  /// could not be read stay in the box, with why, to fix and read again.
  /// How many lines were read; null when it failed (said).
  Future<int?> _read() async {
    setState(() => (_reading = true, _error = null));
    try {
      final r = await _auth.api.readPaymentList(body: ReadPaymentListRequest(text: _paste.text, from: _fromPhoto ? 'photo' : null));
      if (!mounted) return null;
      final good = r.lines.where((l) => (l.problem ?? '').isEmpty).toList();
      final bad = r.lines.where((l) => (l.problem ?? '').isNotEmpty).toList();
      _add([
        for (final l in good)
          if ((l.payee ?? '').isNotEmpty)
            PayRow(
              who: '@${l.payee}',
              sub: [if ((l.name ?? '').isNotEmpty) l.name!, l.bankName ?? ''].where((w) => w.isNotEmpty).join(' · '),
              ask: {'payee': l.payee},
              kobo: l.amountKobo,
            )
          else
            PayRow(
              who: (l.name ?? '').isNotEmpty ? l.name! : 'Account ••${l.accountNumber!.substring(6)}',
              sub: (l.name ?? '').isNotEmpty ? 'Account ••${l.accountNumber!.substring(6)}' : 'Its name is checked with the bank at Check',
              ask: const {},
              number: l.accountNumber!,
              bank: l.bankName ?? '',
              kobo: l.amountKobo,
            ),
      ]);
      setState(() {
        _unread = bad;
        _paste.text = bad.map((l) => l.text).join('\n');
        if (bad.isEmpty) {
          _pasting = false;
          _fromPhoto = false;
        }
      });
      return good.length;
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
      return null;
    } finally {
      if (mounted) setState(() => _reading = false);
    }
  }

  /// From a photo of a list: read on the phone, put in the box, and read
  /// as a pasted list is. No line read: where the business allows it,
  /// "Read it with AI" offered under the button (I3).
  Future<void> _photo() async {
    setState(() => _unreadPhoto = null);
    final photo = await readPhotoFromPhone(context, title: 'A photo of a list');
    if (photo == null || !mounted) return;
    if (photo.words.isNotEmpty) {
      final n = await _readPhotoWords(photo.words);
      if (n == null || n > 0 || !mounted) return;
    }
    if (await _ai.offered()) {
      if (mounted) setState(() => _unreadPhoto = photo);
      return;
    }
    // Off: as before (a photo with no words said), and for an owner that
    // it can be turned on.
    final said = await _ai.offWords(photo.words.isEmpty ? noWordsInPhoto : null);
    if (mounted && said != null) showTkToast(context, said);
  }

  /// A photo's words put in the box and read as a pasted list is.
  Future<int?> _readPhotoWords(String words) async {
    setState(() => (_pasting = true, _unread = const []));
    _paste.text = words;
    _fromPhoto = true;
    return _read();
  }

  /// The photo the phone could not read, sent to Taskiem's AI, and its
  /// words read as the phone's are.
  Future<void> _readWithAI() async {
    final photo = _unreadPhoto;
    if (photo == null || _aiBusy || _busy) return;
    setState(() => _aiBusy = true);
    final words = await readPhotoWithAI(context, _auth, photo);
    if (!mounted) return;
    setState(() => _aiBusy = false);
    if (words == null) return;
    setState(() => _unreadPhoto = null);
    await _readPhotoWords(words);
  }

  Future<void> _pickFor(PayRow r) async {
    final v = await pickOneOf(context, title: 'What is it for?', value: r.purpose, choices: [for (final (w, k) in payPurposes) (k, _capital(w), '')]);
    if (v != null && mounted) setState(() => r.purpose = v);
  }

  /// Every row's purpose at once.
  void _forAll(String key) => setState(() {
    for (final r in _rows) {
      r.purpose = key;
    }
  });

  Future<void> _pickBank(PayRow r) async {
    try {
      final banks = (await _auth.api.listPayBanks(account: r.number)).banks;
      if (!mounted) return;
      final v = await pickPayBank(context, banks, value: r.bank.isEmpty ? null : r.bank);
      if (v != null && mounted) setState(() => r.bank = v);
    } on Object catch (e) {
      if (mounted) showTkToast(context, errorWords(e), error: true);
    }
  }

  /// The rows prepared as a batch: nothing is sent. Those held out are
  /// marked on their row, with why.
  Future<void> _check() async {
    setState(() => (_busy = true, _error = null));
    for (final r in _rows) {
      r.held = null;
    }
    try {
      final b = (await _auth.api.createPaymentBatch(
        body: CreatePaymentBatchRequest(
          rows: [for (final r in _rows) r.body()],
          kind: widget.kind,
          title: widget.title.isEmpty ? null : widget.title,
          from: 'tools',
        ),
      )).batch;
      if (!mounted) return;
      for (final h in b.held ?? const <PaymentBatchHeldItem>[]) {
        final i = h.row ?? -1;
        if (i >= 0 && i < _rows.length) _rows[i].held = h.problem ?? 'It cannot be paid as it is.';
      }
      if (b.count == 0 && b.status != 'preparing') {
        // None can go: nothing is left waiting, and each says why.
        unawaited(_auth.api.cancelPaymentBatch(id: b.id).then((_) {}, onError: (Object _) {}));
        setState(() => _error = 'None of them can be paid as they are: see each one.');
        return;
      }
      setState(() => _batch = b);
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Widget _rowCard(PayRow r) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    return TkFormGroup(
      children: [
        Row(
          children: [
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    r.who,
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                    style: tkText(size: 16, weight: 600, lineHeight: 1.3, color: t.text),
                  ),
                  if (r.sub.isNotEmpty)
                    Text(
                      r.sub,
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                      style: tkText(size: 12.5, lineHeight: 1.35, color: t.muted),
                    ),
                ],
              ),
            ),
            const SizedBox(width: 8),
            TkSmallButton(label: 'Remove', semantics: 'Remove ${r.who}', onPressed: _busy ? null : () => _remove(r)),
          ],
        ),
        if (r.held != null)
          TkCallout(
            leading: TkSquare('b-alert', color: look.orange),
            title: 'Held out',
            sub: r.held,
            subLines: 4,
            color: look.orange,
          ),
        if (r.number.isNotEmpty)
          TkKitField(
            label: 'Bank',
            field: false,
            child: TkPickBox(label: 'Bank for ${r.who}', shown: r.bank.isEmpty ? 'Choose the bank' : r.bank, onTap: () => unawaited(_pickBank(r))),
          ),
        TkKitField(
          label: 'How much?',
          child: TkTextBox(controller: r.amount, money: true, prefix: '₦', size: 15, height: 46),
        ),
        TkKitField(
          label: 'What for?',
          field: false,
          child: TkPickBox(
            label: 'What ${r.who} is paid for',
            shown: r.purpose.isEmpty ? 'Choose' : _capital(purposeSaid(r.purpose)),
            onTap: () => unawaited(_pickFor(r)),
          ),
        ),
      ],
    );
  }

  @override
  Widget build(BuildContext context) {
    final b = _batch;
    if (b != null) {
      return BatchSheet(
        key: ValueKey(b.id),
        auth: _auth,
        batch: b,
        fresh: true,
        follow: widget.follow,
        held: [
          for (final r in _rows)
            if (r.held != null) (who: r.who, problem: r.held!),
        ],
        onRemoveHeld: (who) {
          final r = _rows.where((r) => r.held != null && r.who == who).firstOrNull;
          if (r != null) _remove(r);
        },
        onBack: () => setState(() => _batch = null),
      );
    }
    final look = TkLook.of(context);
    final problem = _problem;
    final purposes = {for (final r in _rows) r.purpose};
    return TkFormSheet(
      title: 'Pay several people',
      crumb: 'Each checked with the bank before you pay',
      foot: _rows.isEmpty ? null : '${peopleWords(_rows.length)} · ${naira(_total)}',
      action: _busy ? 'Checking…' : 'Check',
      onAction: _busy || problem != null ? null : _check,
      second: 'Cancel',
      onSecond: () => Navigator.of(context).pop(),
      children: [
        if (_rows.isEmpty && !_pasting)
          TkCallout(
            leading: TkSquare('b-people', color: look.teal),
            title: 'Add the people to pay',
            sub: 'Pick saved accounts, riders or staff, or paste a list or read one from a photo: one person a line.',
            color: look.teal,
          ),
        if (widget.note != null)
          TkCallout(
            leading: TkSquare('b-alert', color: look.orange),
            title: widget.note!,
            color: look.orange,
          ),
        TkButtonRow([
          TkSmallButton(label: 'Saved', icon: 'b-bank', semantics: 'Add saved accounts', onPressed: _picking || _busy ? null : () => unawaited(_pickSaved())),
          TkSmallButton(
            label: 'Riders',
            icon: 'b-bike',
            semantics: 'Add riders, with what each is due',
            onPressed: _picking || _busy ? null : () => unawaited(_pickRiders()),
          ),
        ]),
        // Four side by side do not fit a phone: two rows of two.
        TkButtonRow([
          TkSmallButton(
            label: 'Staff',
            icon: 'b-people',
            semantics: 'Add staff, to the account linked to each',
            onPressed: _picking || _busy ? null : () => unawaited(_pickStaff()),
          ),
          TkSmallButton(label: 'Paste', icon: 'b-note', semantics: 'Paste a list', onPressed: _busy ? null : () => setState(() => _pasting = !_pasting)),
        ]),
        PhotoReadButton(
          label: 'From a photo of a list',
          semantics: 'Add people from a photo of a list. Read on this phone, not sent',
          onPressed: _busy || _reading || _aiBusy || readingPhoto ? null : () => unawaited(_photo()),
        ),
        if (_unreadPhoto case final photo?)
          PhotoAICallout(
            title: photo.words.isEmpty ? 'No words found' : 'No line of the list read',
            busy: _aiBusy || _reading,
            onPressed: () => unawaited(_readWithAI()),
          ),
        if (_pasting) ...[
          TkFormGroup(
            children: [
              TkKitField(
                label: 'One person a line',
                hint: 'Like: Chidi 0123456789 GTBank 45k',
                child: TkTextBox(controller: _paste, lines: 5, size: 15, height: 46, plain: true),
              ),
              Align(
                alignment: Alignment.centerLeft,
                child: TkSmallButton(
                  label: _reading ? 'Reading…' : 'Read the list',
                  primary: true,
                  onPressed: _reading || _paste.text.trim().isEmpty ? null : () => unawaited(_read()),
                ),
              ),
            ],
          ),
          for (final l in _unread)
            TkCallout(
              leading: TkSquare('b-alert', color: look.orange),
              title: 'Line ${l.line}: ${l.text}',
              sub: '${l.problem} Fix it in the box and read it again.',
              subLines: 3,
              color: look.orange,
            ),
        ],
        if (_rows.length > 1)
          TkFormGroup(
            children: [
              TkKitField(
                label: 'What is it for? (for all)',
                field: false,
                child: TkChips<String>(
                  label: 'What it is for, for all',
                  padding: EdgeInsets.zero,
                  chips: [for (final (w, k) in payPurposes) (k, w)],
                  on: purposes.length == 1 ? purposes.single : '',
                  onPick: _forAll,
                ),
              ),
            ],
          ),
        for (final r in _rows) KeyedSubtree(key: ObjectKey(r), child: _rowCard(r)),
        if (problem != null && problem.isNotEmpty && _rows.every((r) => r.amount.text.trim().isNotEmpty))
          TkCallout(
            leading: TkSquare('b-alert', color: look.orange),
            title: problem,
            color: look.orange,
          ),
        if (_error != null)
          TkCallout(
            leading: TkSquare('b-x', color: look.red),
            title: 'Not ready to pay',
            sub: _error,
            subLines: 4,
            color: look.red,
          ),
      ],
    );
  }
}

/// A batch in words, and its tone: from the list's counts (its payments
/// are read when it is opened).
({String words, CardTone tone}) batchStatus(PaymentBatch b) {
  final live = b.payments.any((p) => p.status == 'sending' || p.status == 'unknown');
  return switch (b.status) {
    'cancelled' => (words: 'Cancelled', tone: CardTone.plain),
    'preparing' => (words: 'Being checked', tone: CardTone.plain),
    'prepared' => (words: 'Not sent yet', tone: CardTone.warn),
    _ when b.failed > 0 => (words: '${b.failed} not paid', tone: CardTone.danger),
    _ when b.waiting > 0 => (words: '${b.waiting} waiting for an OK', tone: CardTone.warn),
    _ when live || b.paidKobo < b.totalKobo => (words: 'Sending', tone: CardTone.plain),
    _ => (words: 'Paid', tone: CardTone.lime),
  };
}

/// A batch's rows to confirm, with one passkey for the batch (money.pay.
/// batch on `batch:<id>`): [payments] the rows the person saw, [again] the
/// rows they said to pay again. Asked first with no proof, so what stops
/// it is said before the passkey is asked for.
Future<PaymentBatch> confirmBatchWithProof(Auth auth, String id, {required List<String> payments, List<String> again = const []}) async {
  Future<PaymentBatch> send(Map<String, dynamic>? passkey) async {
    final r = await auth.transport.send(
      'POST',
      '/v1/payment-batches/${Uri.encodeComponent(id)}/confirm',
      body: {'passkey': ?passkey, 'payments': payments, if (again.isNotEmpty) 'again': again},
    );
    return ConfirmPaymentBatchResponse.fromJson((r as Map).cast<String, dynamic>()).batch;
  }

  try {
    return await send(null);
  } on ApiError catch (e) {
    if (e.status != 403 || e.body['step_up'] == null) rethrow;
  }
  return send(await passkeyProof(auth, payOpBatch, 'batch:$id'));
}

/// The owner's OK for [payments], the rows shown waiting, with one passkey
/// (money.pay.approve on `batch:<id>`).
Future<PaymentBatch> approveBatchWithProof(Auth auth, String id, {required List<String> payments}) async {
  Future<PaymentBatch> send(Map<String, dynamic>? passkey) async {
    final r = await auth.transport.send('POST', '/v1/payment-batches/${Uri.encodeComponent(id)}/approve', body: {'passkey': ?passkey, 'payments': payments});
    return ApprovePaymentBatchResponse.fromJson((r as Map).cast<String, dynamic>()).batch;
  }

  try {
    return await send(null);
  } on ApiError catch (e) {
    if (e.status != 403 || e.body['step_up'] == null) rethrow;
  }
  return send(await passkeyProof(auth, 'money.pay.approve', 'batch:$id'));
}

const payOpBatch = 'money.pay.batch';

/// A batch as a sheet: how many and the total, large; the rows held out
/// (amber, with why); a row per person filling in as each goes. Not sent
/// yet: Pay with one passkey, and Back or Cancel it. Sent: the failed ones
/// paid again, the owner's OK for those waiting, or the same again.
class BatchSheet extends StatefulWidget {
  const BatchSheet({
    super.key,
    required this.auth,
    required this.batch,
    this.fresh = false,
    this.held = const [],
    this.onRemoveHeld,
    this.onBack,
    this.follow = true,
  });
  final Auth auth;
  final PaymentBatch batch;

  /// Prepared in this sheet just now: let go if the sheet closes unpaid.
  final bool fresh;

  /// The rows held out when it was prepared, by who.
  final List<({String who, String problem})> held;
  final ValueChanged<String>? onRemoveHeld;

  /// Back to the list it was prepared from (the batch let go).
  final VoidCallback? onBack;

  /// Read again while rows are on their way.
  final bool follow;

  @override
  State<BatchSheet> createState() => _BatchSheetState();
}

class _BatchSheetState extends State<BatchSheet> {
  late PaymentBatch _b = widget.batch;
  late List<({String who, String problem})> _held = [...widget.held];
  late String? _freshId = widget.fresh ? widget.batch.id : null;
  // What became of each row when sent or approved, by payment id.
  final Map<String, PaymentBatchResultsItem> _results = {};
  // Rows the person said to pay again (the same payment in the last day).
  final Set<String> _again = {};
  final List<String> _notes = [];
  bool _busy = false;
  bool _tried = false;
  bool _sent = false;
  String? _error;
  Timer? _timer;

  Auth get _auth => widget.auth;

  bool get _owner => (_auth.me?.roles as List?)?.contains('owner') ?? false;

  @override
  void initState() {
    super.initState();
    _follow();
  }

  @override
  void dispose() {
    _timer?.cancel();
    if (_freshId == _b.id && !_tried && (_b.status == 'prepared' || _b.status == 'preparing')) {
      unawaited(_auth.api.cancelPaymentBatch(id: _b.id).then((_) {}, onError: (Object _) {}));
    }
    super.dispose();
  }

  List<Payment> get _rows => _b.payments.where((p) => p.status != 'cancelled').toList();

  List<Payment> get _prepared => _b.payments.where((p) => p.status == 'prepared').toList();

  bool get _live => _b.status == 'preparing' || _b.payments.any((p) => p.status == 'sending' || p.status == 'unknown' || p.status == 'waiting_ok');

  /// Read again in a while, while anything is on its way.
  void _follow() {
    _timer?.cancel();
    if (!widget.follow || !_live) return;
    _timer = Timer(Duration(seconds: _b.payments.any((p) => p.status == 'sending' || p.status == 'unknown') || _b.status == 'preparing' ? 3 : 15), () async {
      try {
        final b = (await _auth.api.getPaymentBatch(id: _b.id)).batch;
        if (mounted) setState(() => _b = b);
      } on Object {
        // Read again next time.
      }
      if (mounted) _follow();
    });
  }

  void _took(PaymentBatch b) {
    for (final r in b.results ?? const <PaymentBatchResultsItem>[]) {
      if (r.payment != null) _results[r.payment!] = r;
    }
    _b = b;
  }

  Future<void> _pay() async {
    setState(() => (_busy = true, _error = null));
    try {
      if (!await passkeys.available()) {
        setState(() => _error = 'Paying several people takes a passkey: add one on this phone in Settings.');
        return;
      }
      _tried = true;
      final ids = [for (final p in _prepared) p.id];
      final b = await confirmBatchWithProof(
        _auth,
        _b.id,
        payments: ids,
        again: [
          for (final id in ids)
            if (_again.contains(id)) id,
        ],
      );
      if (!mounted) return;
      setState(() {
        for (final id in ids) {
          _results.remove(id);
        }
        _took(b);
        _again.removeAll(ids);
        _sent = true;
      });
      _follow();
      await Future<void>.delayed(const Duration(milliseconds: 1200));
      if (mounted) setState(() => _sent = false);
    } on ApiError catch (e) {
      setState(() => _error = e.message);
      if (e.body['code'] == 'changed' || e.body['code'] == 'not_prepared') unawaited(_reload());
    } on PasskeyCancelled {
      // The passkey sheet put away: the batch waits.
    } on Object catch (e) {
      setState(() => _error = errorWords(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _reload() async {
    try {
      final b = (await _auth.api.getPaymentBatch(id: _b.id)).batch;
      if (mounted) setState(() => _b = b);
    } on Object {
      // Shown as it was.
    }
  }

  /// The rows waiting for an OK that this owner may give.
  List<Payment> get _toApprove =>
      _b.payments.where((p) => p.status == 'waiting_ok' && _results[p.id]?.code != 'own' && _results[p.id]?.code != 'changed').toList();

  Future<void> _approve() async {
    setState(() {
      _busy = true;
      _error = null;
      _notes.clear();
    });
    try {
      if (!await passkeys.available()) {
        setState(() => _error = 'Your OK takes a passkey: add one on this phone in Settings.');
        return;
      }
      final b = await approveBatchWithProof(_auth, _b.id, payments: [for (final p in _toApprove) p.id]);
      if (!mounted) return;
      final results = b.results ?? const <PaymentBatchResultsItem>[];
      final own = results.where((r) => r.code == 'own').length;
      final changed = results.where((r) => r.code == 'changed').length;
      setState(() {
        _took(b);
        if (own > 0) _notes.add('$own you confirmed yourself: another owner gives ${own == 1 ? 'its' : 'their'} OK.');
        if (changed > 0) {
          _notes.add('$changed to an account changed since it was last paid: give ${changed == 1 ? 'it' : 'each'} the OK on its own in Approvals.');
        }
      });
      showTkToast(context, 'Given the OK: on their way');
      _follow();
    } on ApiError catch (e) {
      setState(() => _error = e.message);
    } on PasskeyCancelled {
      // Put away: they still wait.
    } on Object catch (e) {
      setState(() => _error = errorWords(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  /// A new batch to check and pay: the failed rows again, or the whole of
  /// this one (the same as last time).
  Future<void> _anew(Future<PaymentBatch> Function() make) async {
    setState(() => (_busy = true, _error = null));
    try {
      final b = await make();
      if (!mounted) return;
      setState(() {
        _b = b;
        _freshId = b.id;
        _tried = false;
        _results.clear();
        _again.clear();
        _notes.clear();
        _held = [for (final h in b.held ?? const <PaymentBatchHeldItem>[]) (who: h.payee ?? 'Someone', problem: h.problem ?? '')];
      });
      _follow();
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _cancel() async {
    setState(() => (_busy = true, _error = null));
    try {
      final b = (await _auth.api.cancelPaymentBatch(id: _b.id)).batch;
      _b = b;
      if (!mounted) return;
      final sent = b.payments.any((p) => p.status != 'cancelled');
      showTkToast(context, sent ? 'The rest let go: not paid' : 'Cancelled: nothing was paid');
      Navigator.of(context).pop(b);
    } on Object catch (e) {
      if (mounted) setState(() => _error = errorWords(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Widget _row(Payment p) {
    final look = TkLook.of(context);
    final res = _results[p.id];
    final problem = (res?.problem ?? '').isNotEmpty && p.status == 'prepared' ? res!.problem! : '';
    final s = problem.isNotEmpty ? (words: 'Not sent', tone: CardTone.warn) : paymentStatus(p.status);
    final reason = p.reason is String ? p.reason as String : '';
    final sub = [
      if (problem.isNotEmpty) problem else if (p.status == 'failed' && reason.isNotEmpty) reason else ...[purposeWords(p), p.account],
    ].join(' · ');
    final again = res?.code == 'pay_again' && p.status == 'prepared';
    final row = TkListRow(
      title: paymentName(p),
      letters: paymentName(p),
      sub: sub,
      subLines: 2,
      titleLines: 1,
      amount: naira(p.amountKobo),
      status: s.words,
      statusTone: s.tone,
      waiting: p.status == 'waiting_ok',
      semanticsLabel: '${paymentName(p)}, ${naira(p.amountKobo)}, ${s.words}. $sub.',
      onTap: p.status == 'prepared' ? null : () => unawaited(showTkSheet<Payment>(context, (_) => PaymentSheet(auth: _auth, payment: p))),
    );
    if (!again) return row;
    final on = _again.contains(p.id);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        row,
        Padding(
          padding: const EdgeInsets.fromLTRB(12, 0, 12, 10),
          child: TkCallout(
            leading: TkSquare('b-refund', color: look.orange),
            title: on ? 'Paying again' : 'Paid the same in the last day',
            sub: on ? 'It goes with the rest.' : 'Pay again only if this is a second payment.',
            color: look.orange,
            button: on ? 'Leave it' : 'Pay again',
            semantics: on ? 'Do not pay ${paymentName(p)} again' : 'Pay ${paymentName(p)} again',
            onPressed: _busy ? null : () => setState(() => on ? _again.remove(p.id) : _again.add(p.id)),
          ),
        ),
      ],
    );
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final b = _b;
    final rows = _rows;
    final prepared = _prepared;
    final open = b.status == 'prepared';
    final preparing = b.status == 'preparing';
    final toSend = prepared.fold(0, (n, p) => n + p.amountKobo);
    final status = batchStatus(b);
    final facts = [
      if (b.paidKobo > 0) '${naira(b.paidKobo)} paid',
      if (b.waiting > 0) '${b.waiting} waiting for an OK',
      if (b.failed > 0) '${b.failed} not paid',
    ];
    final top = TkTopCard(
      semanticsLabel: '${peopleWords(b.count)}, ${naira(b.totalKobo)}. ${open ? 'Not sent yet' : status.words}.',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          TkEyebrow(
            open || preparing ? 'Paying' : status.words,
            color: look.limeInk,
            icon: 'b-people',
            trailing: b.title.isEmpty || !(open || preparing) ? null : b.title,
          ),
          const SizedBox(height: 8),
          Text(peopleWords(b.count), style: tkDisplay(size: 28, lineHeight: 1.15, letterSpacing: -0.8, color: t.text)),
          const SizedBox(height: 10),
          FittedBox(
            fit: BoxFit.scaleDown,
            alignment: Alignment.centerLeft,
            child: Text(naira(b.totalKobo), style: tkFigure(context, size: 34)),
          ),
          if (facts.isNotEmpty) ...[const SizedBox(height: 4), Text(facts.join(' · '), style: tkText(size: 14, lineHeight: 1.35, color: t.muted))],
        ],
      ),
    );
    final children = <Widget>[
      top,
      for (final h in _held)
        TkCallout(
          leading: TkSquare('b-alert', color: look.orange),
          title: 'Held out: ${h.who}',
          sub: h.problem,
          subLines: 4,
          color: look.orange,
          button: 'Remove',
          semantics: 'Remove ${h.who}',
          onPressed: () {
            setState(() => _held.remove(h));
            widget.onRemoveHeld?.call(h.who);
          },
        ),
      for (final n in _notes)
        TkCallout(
          leading: TkSquare('b-alert', color: look.teal),
          title: n,
          subLines: 3,
          color: look.teal,
        ),
      if (preparing)
        TkCallout(
          leading: TkSquare('b-clock', color: look.teal),
          title: 'Checking each one with the bank…',
          color: look.teal,
        ),
      TkSection(title: 'Each person', count: open && prepared.isNotEmpty ? prepared.length : null, children: [for (final p in rows) _row(p)]),
      if (_error != null)
        TkCallout(
          leading: TkSquare('b-x', color: look.red),
          title: open ? 'Not paid' : 'Not done',
          sub: _error,
          subLines: 4,
          color: look.red,
        ),
    ];
    final overlay = _sent ? const TkSavedTick(words: 'Sent') : null;
    if (open || preparing) {
      final again = prepared.where((p) => _again.contains(p.id)).length;
      final partly = b.payments.any((p) => p.status != 'prepared' && p.status != 'cancelled');
      return TkFormSheet(
        title: 'Check before you pay',
        crumb: 'Pay several people',
        overlay: overlay,
        foot: [
          '${peopleWords(prepared.length)} to pay · ${naira(toSend)}',
          if (_held.isNotEmpty) '${_held.length} held out',
          if (again > 0) '$again again',
        ].join(' · '),
        action: _busy ? 'Paying…' : 'Pay ${peopleWords(prepared.length)}',
        actionIcon: 'fingerprint',
        onAction: _busy || preparing || prepared.isEmpty ? null : _pay,
        second: widget.onBack != null && !_tried ? 'Back' : (partly ? 'Let the rest go' : 'Cancel it'),
        secondDanger: widget.onBack == null || _tried,
        onSecond: _busy ? null : (widget.onBack != null && !_tried ? widget.onBack : _cancel),
        children: children,
      );
    }
    final ok = _owner && _toApprove.isNotEmpty;
    final failed = b.failed > 0;
    final (String action, VoidCallback? onAction) = ok
        ? (_busy ? 'Giving the OK…' : 'Give the OK for all', _busy ? null : _approve)
        : failed
        ? (
            _busy ? 'Preparing…' : 'Pay the failed ones again',
            _busy ? null : () => unawaited(_anew(() async => (await _auth.api.payBatchAgain(id: _b.id)).batch)),
          )
        : (
            _busy ? 'Preparing…' : 'Same as last time',
            _busy || b.status == 'cancelled'
                ? null
                : () => unawaited(
                    _anew(
                      () async => (await _auth.api.createPaymentBatch(
                        body: CreatePaymentBatchRequest(copyOf: _b.id, from: 'tools'),
                      )).batch,
                    ),
                  ),
          );
    return TkFormSheet(
      title: b.title.isEmpty ? 'Paid together' : b.title,
      crumb: 'Pay several people',
      overlay: overlay,
      foot: ok ? '${_toApprove.length} waiting for your OK · ${naira(_toApprove.fold(0, (n, p) => n + p.amountKobo))}' : null,
      action: action,
      actionIcon: ok ? 'fingerprint' : null,
      onAction: onAction,
      second: 'Done',
      onSecond: () => Navigator.of(context).pop(_b),
      children: children,
    );
  }
}

/// Several of [choices] (key, title, sub) picked at once: the keys, or
/// null when put away. [off] (title, why) are shown under them, not to be
/// picked; [empty] is said when there are no choices.
Future<Set<String>?> pickSeveral(
  BuildContext context, {
  required String title,
  required List<(String, String, String)> choices,
  String icon = 'b-person',
  List<(String, String)> off = const [],
  String? empty,
}) => showTkSheet<Set<String>>(
  context,
  (_) => TkAppLook(
    child: _PickSeveral(title: title, choices: choices, icon: icon, off: off, empty: empty),
  ),
);

class _PickSeveral extends StatefulWidget {
  const _PickSeveral({required this.title, required this.choices, required this.icon, this.off = const [], this.empty});
  final String title;
  final List<(String, String, String)> choices;
  final String icon;
  final List<(String, String)> off;
  final String? empty;

  @override
  State<_PickSeveral> createState() => _PickSeveralState();
}

class _PickSeveralState extends State<_PickSeveral> {
  final _picked = <String>{};

  @override
  Widget build(BuildContext context) {
    final n = _picked.length;
    return TkFormSheet(
      title: widget.title,
      crumb: n == 0 ? 'Choose who to pay' : '$n chosen',
      action: n == 0 ? 'Add' : 'Add $n',
      onAction: n == 0 ? null : () => Navigator.of(context).pop(_picked),
      second: 'Cancel',
      onSecond: () => Navigator.of(context).pop(),
      children: [
        TkSection(
          empty: widget.empty ?? 'All of them are in the list already.',
          children: [
            for (final (key, title, sub) in widget.choices)
              Semantics(
                checked: _picked.contains(key),
                child: TkListRow(
                  title: title,
                  sub: sub,
                  subLines: 1,
                  leading: TkCheckbox(checked: _picked.contains(key)),
                  chevron: false,
                  semanticsLabel: '$title${_picked.contains(key) ? ', chosen' : ''}',
                  onTap: () => setState(() => _picked.contains(key) ? _picked.remove(key) : _picked.add(key)),
                ),
              ),
          ],
        ),
        if (widget.off.isNotEmpty)
          TkSection(
            title: 'Not payable yet',
            children: [
              for (final (title, why) in widget.off)
                TkListRow(title: title, sub: why, subLines: 2, icon: 'b-lock', square: TkLook.of(context).orange, chevron: false),
            ],
          ),
      ],
    );
  }
}
