// The storefront's bookings (M6 part O; web/src/pages/StorefrontBookings.
// tsx, lib/bookings.ts), opened from Services' "Open bookings": upcoming
// bookings by day with Move and Cancel; the services customers book; the
// hours and days off they book in; and the booking rules. Times are in
// Lagos time. Which table holds the services stays a set-up for the web
// or @Taskiem.

import 'package:flutter/widgets.dart';

import '../api/transport.dart';
import '../app/auth.dart';
import '../kit/forms.dart' show TkSwitchRow, errorWords;
import '../kit/kit.dart';
import '../kit/modal.dart';
import '../kit/page.dart';
import '../app/links.dart' show callPhone, textPhone;
import '../kit/list.dart' show TkLoading;
import '../kit/look.dart';
import '../kit/mobile.dart';
import '../kit/sheet.dart' show TkKitField, showTkSheet;
import '../kit/tap_area.dart';
import '../theme/look.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'storefront_orders_page.dart' show groupedPhone;
import 'book_sheet.dart' show dayLabel, formatDuration, lagosDay, lagosTime, servicePrice, whenLabel;
import 'saved_fill.dart' show SavedButton, pickSavedDate;
import 'words.dart' show Tone;

List<Map<String, dynamic>> _maps(Object? v) => (v as List? ?? const []).whereType<Map<String, dynamic>>().toList();
int? _int(Object? v) => (v as num?)?.toInt();

const days = ['Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday', 'Sunday'];
const maxOpenings = 3;
const aheadDefault = 30;
final _hhmm = RegExp(r'^([01][0-9]|2[0-3]):[0-5][0-9]$');
final _date = RegExp(r'^\d{4}-\d{2}-\d{2}$');

/// A real day as typed, 2026-12-25 (not 2026-02-30).
bool isDay(String d) {
  if (!_date.hasMatch(d)) return false;
  final p = DateTime.tryParse('${d}T12:00:00Z');
  return p != null && addDays(d, 0) == d;
}

/// bookings.ts MAX_DAYS_OFF.
const maxDaysOff = 400;
int _minutes(String hhmm) => int.parse(hhmm.substring(0, 2)) * 60 + int.parse(hhmm.substring(3, 5));

/// orders.ts ORDER_STATUS: a booking's state in words, and its tone.
({String label, Tone tone}) orderStatus(String s) => switch (s) {
  'received' => (label: 'New', tone: Tone.warn),
  'awaiting_payment' => (label: 'Waiting for payment', tone: Tone.warn),
  'paid' => (label: 'Paid', tone: Tone.info),
  'accepted' => (label: 'Accepted', tone: Tone.info),
  'completed' => (label: 'Completed', tone: Tone.ok),
  'refund_due' => (label: 'Refund due', tone: Tone.danger),
  'price_changed' => (label: 'Waiting for the customer to confirm new prices', tone: Tone.warn),
  'no_show' => (label: 'No-show', tone: Tone.muted),
  'declined' => (label: 'Declined', tone: Tone.muted),
  'expired' => (label: 'Expired', tone: Tone.muted),
  'cancelled' => (label: 'Cancelled', tone: Tone.muted),
  'refunded' => (label: 'Refunded', tone: Tone.muted),
  'dispatched' => (label: 'With a rider', tone: Tone.info),
  'on_the_way' => (label: 'On the way', tone: Tone.info),
  'arrived' => (label: 'At the door', tone: Tone.info),
  'proposed' => (label: 'Waiting for the business to confirm', tone: Tone.info),
  'changed' => (label: 'Waiting for the business to accept your change', tone: Tone.info),
  _ => (label: s.replaceAll('_', ' '), tone: Tone.muted),
};

/// bookings.ts canChange: a booking that still holds its time.
bool canChange(Map<String, dynamic> b) => b['kind'] == 'booking' && const ['received', 'accepted', 'awaiting_payment', 'paid'].contains(b['status']);

/// bookings.ts timeSpan: "10:30–11:15".
String timeSpan(Map<String, dynamic> b) {
  final s = b['starts_at'] as String?;
  if (s == null) return '';
  final e = b['ends_at'] as String?;
  return e == null ? lagosTime(s) : '${lagosTime(s)}–${lagosTime(e)}';
}

/// bookings.ts addDays.
String addDays(String day, int n) {
  final d = DateTime.parse('${day}T12:00:00Z').add(Duration(days: n));
  return '${d.year}-${d.month.toString().padLeft(2, '0')}-${d.day.toString().padLeft(2, '0')}';
}

/// bookings.ts groupByDay: bookings by Lagos day, soonest first.
List<({String day, String label, List<Map<String, dynamic>> bookings})> groupByDay(List<Map<String, dynamic>> list) {
  final timed = list.where((b) => b['starts_at'] is String).toList()
    ..sort((x, y) => DateTime.parse(x['starts_at'] as String).compareTo(DateTime.parse(y['starts_at'] as String)));
  final out = <({String day, String label, List<Map<String, dynamic>> bookings})>[];
  for (final b in timed) {
    final day = lagosDay(b['starts_at'] as String);
    if (out.isEmpty || out.last.day != day) out.add((day: day, label: dayLabel(day), bookings: []));
    out.last.bookings.add(b);
  }
  return out;
}

/// bookings.ts Notice: a length of time as typed, blank for the default.
class Notice {
  Notice(String value, this.hours) : text = TextEditingController(text: value);
  final TextEditingController text;
  bool hours;

  /// bookings.ts noticeToForm.
  factory Notice.of(int? minutes) =>
      minutes == null || minutes == 0 ? Notice('', true) : (minutes % 60 == 0 ? Notice('${minutes ~/ 60}', true) : Notice('$minutes', false));

  /// bookings.ts noticeMinutes: 0 when blank; null when not a number.
  int? get minutes {
    final v = text.text.trim();
    if (v.isEmpty) return 0;
    if (!RegExp(r'^\d+(\.\d+)?$').hasMatch(v)) return null;
    return (double.parse(v) * (hours ? 60 : 1)).round();
  }
}

/// StorefrontBookings.tsx CancelDialog: asks first, with Keep it.
Future<bool> askCancelBooking(BuildContext context, Map<String, dynamic> b) => showTkConfirm(
  context,
  title: 'Cancel booking ${b['reference']}?',
  body:
      '${b['service_name'] ?? ''}${b['starts_at'] != null ? ', ${whenLabel(b['starts_at'] as String)}' : ''}. The time is freed and the customer is texted that you cancelled.'
      '${b['paid_at'] != null ? ' They have paid, so the booking shows as Refund due until you refund them in Paystack.' : ''}',
  ok: 'Cancel the booking',
  cancel: 'Keep it',
  danger: true,
);

/// Cancels [b]; answers what to say.
Future<String> cancelBooking(Auth auth, Map<String, dynamic> b) async {
  final r = (await auth.transport.send('POST', '/v1/storefront/bookings/${Uri.encodeComponent(b['reference'] as String)}/cancel', body: const {}) as Map)['booking'] as Map;
  return r['status'] == 'refund_due' ? '${r['reference']} is cancelled. They had paid: refund them in Paystack.' : '${r['reference']} is cancelled; the customer is told.';
}

/// StorefrontBookings.tsx MoveDialog: a new day and time; answers what to
/// say, or null when it was not moved.
Future<String?> moveBooking(BuildContext context, Auth auth, Map<String, dynamic> b) => showTkSheet<String>(context, (_) => BookingSheet(auth: auth, booking: b));

/// The move by a typed day and time, where the free times cannot be read.
Future<String?> typeMove(BuildContext context, Auth auth, Map<String, dynamic> b) async {
  final moved = await showTkModal<Map<String, dynamic>>(context, title: 'Move booking ${b['reference']}', builder: (_) => _MoveForm(auth: auth, booking: b));
  if (moved == null) return null;
  return '${moved['reference']} is moved to ${moved['starts_at'] != null ? whenLabel(moved['starts_at'] as String) : 'its new time'}; the customer is told.';
}

/// A booking period as typed.
class _Span {
  _Span(String open, String close) : open = TextEditingController(text: open), close = TextEditingController(text: close);
  final TextEditingController open;
  final TextEditingController close;
}

class _Day {
  _Day(this.open, this.spans);
  bool open;
  final List<_Span> spans;
}

class BookingsPage extends StatefulWidget {
  const BookingsPage({super.key, required this.auth});
  final Auth auth;

  @override
  State<BookingsPage> createState() => _BookingsPageState();
}

class _BookingsPageState extends State<BookingsPage> {
  Map<String, dynamic>? _sf;
  bool _noStorefront = false;
  String? _error;
  String _range = 'month';
  List<Map<String, dynamic>>? _bookings;
  bool _noBookings = false;
  List<Map<String, dynamic>>? _services;
  String? _servicesError;
  String _notice = '';
  String _settingsNotice = '';
  bool _busy = false;
  String? _saveError;

  // The availability as the editor holds it (bookings.ts AvailabilityForm).
  bool _sameAsHours = true;
  List<_Day> _days = [];
  List<String> _off = [];
  final _ahead = TextEditingController();
  Notice _soonest = Notice.of(null);
  Notice _cutoff = Notice.of(null);
  bool _autoConfirm = false;
  final _newOff = TextEditingController();
  String _was = '';

  Transport get _t => widget.auth.transport;

  @override
  void initState() {
    super.initState();
    _ahead.addListener(() => setState(() {}));
    _newOff.addListener(() => setState(() {}));
    _load();
  }

  /// The editor's own boxes, let go of.
  List<TextEditingController> get _boxes => [
    for (final d in _days)
      for (final s in d.spans) ...[s.open, s.close],
    _soonest.text,
    _cutoff.text,
  ];

  @override
  void dispose() {
    for (final c in [..._boxes, _ahead, _newOff]) {
      c.dispose();
    }
    super.dispose();
  }

  String _why(Object e) => e is ApiError ? e.message : 'You’re offline. This shows when you’re back online.';

  Future<void> _load() async {
    setState(() => _error = null);
    try {
      final sf = (await _t.send('GET', '/v1/storefront') as Map).cast<String, dynamic>();
      if (!mounted) return;
      setState(() {
        _sf = sf;
        _fill(sf);
      });
      _loadServices();
    } on ApiError catch (e) {
      if (mounted) setState(() => e.status == 404 ? _noStorefront = true : _error = e.message);
    } on Offline catch (e) {
      if (mounted) setState(() => _error = _why(e));
    }
    await _loadBookings();
  }

  Future<void> _loadServices() async {
    if ((_sf?['services_table'] as String? ?? '').isEmpty) return;
    try {
      final r = await _t.send('GET', '/v1/storefront/services');
      if (mounted) setState(() => (_services = _maps((r as Map)['items']), _servicesError = null));
    } on Object catch (e) {
      if (mounted) setState(() => _servicesError = _why(e));
    }
  }

  Future<void> _loadBookings() async {
    final from = lagosDay(DateTime.now().toUtc().toIso8601String());
    try {
      final r = await _t.send('GET', '/v1/storefront/bookings', query: {'from': from, 'to': addDays(from, _range == 'week' ? 7 : 31)});
      if (mounted) setState(() => (_bookings = _maps((r as Map)['bookings']), _error = null));
    } on ApiError catch (e) {
      if (mounted) setState(() => e.status == 404 ? _noBookings = true : _error = e.message);
    } on Offline catch (e) {
      if (mounted) setState(() => _error = _why(e));
    }
  }

  /// bookings.ts availabilityToForm: with no hours of its own, the days
  /// start from the card's opening hours, to edit from.
  void _fill(Map<String, dynamic> sf) {
    // Those it replaces, once off the screen.
    final old = _boxes;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      for (final c in old) {
        c.dispose();
      }
    });
    final a = (sf['availability'] as Map?)?.cast<String, dynamic>() ?? const {};
    final own = ((a['days'] as List?) ?? const []).any((d) => d is List && d.isNotEmpty);
    final card = ((((sf['draft'] as Map?)?['hours'] as Map?)?['days']) as List?) ?? const [];
    List<Map<String, dynamic>> spansOf(Object? d) => _maps(d);
    _days = [
      for (var i = 0; i < 7; i++)
        () {
          final raw = own
              ? spansOf(((a['days'] as List?) ?? const []).elementAtOrNull(i))
              : spansOf(card.elementAtOrNull(i)).where((s) {
                  final o = s['open'] as String? ?? '', c = s['close'] as String? ?? '';
                  return _hhmm.hasMatch(o) && _hhmm.hasMatch(c) && _minutes(c) > _minutes(o);
                }).toList();
          return raw.isEmpty
              ? _Day(false, [_Span('09:00', '17:00')])
              : _Day(true, [for (final s in raw.take(maxOpenings)) _Span(s['open'] as String? ?? '', s['close'] as String? ?? '')]);
        }(),
    ];
    for (final d in _days) {
      for (final s in d.spans) {
        s.open.addListener(() => setState(() {}));
        s.close.addListener(() => setState(() {}));
      }
    }
    _sameAsHours = !own;
    _off = [...((a['off'] as List?) ?? const []).whereType<String>()]..sort();
    _ahead.text = (_int(a['ahead_days']) ?? 0) > 0 ? '${a['ahead_days']}' : '';
    _soonest = Notice.of(_int(a['soonest_minutes']))..text.addListener(() => setState(() {}));
    _cutoff = Notice.of(_int(a['cutoff_minutes']))..text.addListener(() => setState(() {}));
    _autoConfirm = a['auto_confirm'] == true;
    _was = '${_request()}';
  }

  /// bookings.ts availabilityFromForm, with the services as they are.
  Map<String, Object?> _request() {
    final sf = _sf ?? const {};
    final av = <String, Object?>{
      'days': [
        for (var i = 0; i < 7; i++)
          _sameAsHours || !_days[i].open
              ? <Object>[]
              : [
                  for (final s in _days[i].spans.take(maxOpenings)) {'open': s.open.text, 'close': s.close.text},
                ],
      ],
      if (_off.isNotEmpty) 'off': (_off.toSet().toList()..sort()),
      if ((int.tryParse(_ahead.text.trim()) ?? 0) > 0) 'ahead_days': int.parse(_ahead.text.trim()),
      if ((_soonest.minutes ?? 0) > 0) 'soonest_minutes': _soonest.minutes,
      if ((_cutoff.minutes ?? 0) > 0) 'cutoff_minutes': _cutoff.minutes,
      if (_autoConfirm) 'auto_confirm': true,
    };
    return {'services_table': sf['services_table'], 'services_columns': sf['services_columns'] ?? const {}, 'availability': av};
  }

  /// bookings.ts availabilityErrors.
  List<String> get _problems {
    final out = <String>[];
    if (!_sameAsHours) {
      for (var i = 0; i < 7; i++) {
        final d = _days[i];
        if (!d.open) continue;
        for (final s in d.spans) {
          if (!_hhmm.hasMatch(s.open.text) || !_hhmm.hasMatch(s.close.text)) {
            out.add('Give ${days[i]}’s start and end times.');
            break;
          }
          if (_minutes(s.close.text) <= _minutes(s.open.text)) {
            out.add('Booking hours on ${days[i]} must end after they start, the same day.');
            break;
          }
        }
      }
      if (!_days.any((d) => d.open)) out.add('Open at least one day for bookings, or use your opening hours.');
    }
    final ahead = _ahead.text.trim();
    if (ahead.isNotEmpty && (int.tryParse(ahead) == null || int.parse(ahead) < 1 || int.parse(ahead) > 180)) out.add('How far ahead is 1 to 180 days.');
    for (final (n, name) in [(_soonest, 'How soon'), (_cutoff, 'The change cut-off')]) {
      final m = n.minutes;
      if (m == null || m > 7 * 24 * 60) {
        out.add('$name is at most a week (168 hours).');
      } else if (n.text.text.trim().isNotEmpty && m < 1) {
        out.add('$name is at least 1 minute; leave it empty for 2 hours.');
      }
    }
    return out;
  }

  Future<void> _save() async {
    setState(() => (_busy = true, _saveError = null, _settingsNotice = ''));
    try {
      final out = (await _t.send('PUT', '/v1/storefront/bookings', body: _request()) as Map).cast<String, dynamic>();
      if (!mounted) return;
      final published = (_int(_sf?['published_version']) ?? 0) > 0;
      setState(() {
        _sf = out;
        _fill(out);
        _settingsNotice = published ? 'Saved; your page is updated.' : 'Saved. Bookings show on your page once you publish it and switch on the booking form.';
      });
    } on Object catch (e) {
      if (mounted) setState(() => _saveError = _why(e));
    }
    if (mounted) setState(() => _busy = false);
  }

  Future<void> _makeServices() async {
    setState(() => (_busy = true, _saveError = null));
    try {
      await _t.send('POST', '/v1/storefront/catalogue/make', body: {'kind': 'services'});
      final sf = (await _t.send('GET', '/v1/storefront') as Map).cast<String, dynamic>();
      if (!mounted) return;
      setState(() {
        _sf = sf;
        _fill(sf);
        _settingsNotice = 'Your services list is ready: add what customers can book from Services in the chat.';
      });
      await _loadServices();
    } on Object catch (e) {
      if (mounted) setState(() => _saveError = _why(e));
    }
    if (mounted) setState(() => _busy = false);
  }

  /// One booking's sheet: moved or cancelled there, said here.
  Future<void> _open(Map<String, dynamic> b) async {
    final said = await moveBooking(context, widget.auth, b);
    if (said == null || !mounted) return;
    setState(() => _notice = said);
    await _loadBookings();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final sf = _sf;
    // As drawn (docs/spec/mobile-app-look.md, "The remaining screens"):
    // what is to come large, the range as a switch, each booking by day
    // with a call icon; a booking opens its sheet; then the services,
    // hours and rules.
    return TkPageScreen(
      title: 'Bookings',
      titleSize: 30,
      backLabel: 'Back to Services',
      onRefresh: () => Future.wait([_loadServices(), _loadBookings()]),
      description: 'Appointments customers book on your storefront, and the services and hours they book from. Times are in Lagos time. You confirm each booking in the approval request Taskiem sends you in WhatsApp or Telegram.',
      children: [
        if (_error != null) TkErrorBox(_error!),
        if (_noStorefront) Text('You have no storefront yet. Create your storefront first, then set up bookings here.', style: tkHint(t)),
        if (sf == null && !_noStorefront && _error == null) Text('Loading your storefront…', style: tkHint(t)),
        if (sf != null) ...[..._list(t), _servicesCard(t, sf), _hoursCard(t), _rulesCard(t)],
      ],
    );
  }

  List<Widget> _list(TkTokens t) {
    final look = TkLook.of(context);
    final groups = _bookings == null ? null : groupByDay(_bookings!);
    final all = _bookings ?? const <Map<String, dynamic>>[];
    final toCome = all.where(canChange).length;
    final waiting = all.where((b) => b['status'] == 'received').length;
    ({Color fill, Color ink}) accent(Tone tone) => switch (tone) {
      Tone.warn => (fill: look.orange, ink: look.orangeInk),
      Tone.info => (fill: look.blue, ink: look.blueInk),
      Tone.ok => (fill: look.lime, ink: look.limeInk),
      Tone.danger => (fill: look.red, ink: look.redInk),
      _ => (fill: look.line, ink: t.muted),
    };
    return [
      if (_bookings != null && !_noBookings)
        TkTopCard(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              TkEyebrow(_range == 'week' ? 'This week' : 'Next 30 days', color: look.blueInk, trailing: all.length == 1 ? '1 booking' : '${all.length} bookings'),
              const SizedBox(height: 6),
              Text('$toCome to come', style: tkFigure(context)),
              if (waiting > 0)
                Text('$waiting waiting for you to accept, in WhatsApp or Telegram', style: tkText(size: 13, lineHeight: 1.4, color: t.muted)),
            ],
          ),
        ),
      TkChips<String>(
        label: 'Bookings in',
        padding: EdgeInsets.zero,
        chips: const [('week', 'This week'), ('month', 'Next 30 days')],
        on: _range,
        onPick: (v) {
          if (v == _range) return;
          setState(() => _range = v);
          _loadBookings();
        },
      ),
      if (_notice.isNotEmpty) Semantics(liveRegion: true, child: Text(_notice, style: tkText(size: 14, color: t.text))),
      if (_noBookings)
        Text('This Taskiem does not take storefront bookings yet.', style: tkHint(t))
      else if (groups == null)
        Text('Loading bookings…', style: tkHint(t))
      else if (groups.isEmpty)
        Text('No bookings ${_range == 'week' ? 'this week' : 'in the next 30 days'}.', style: tkHint(t))
      else ...[
        for (final g in groups)
          Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Padding(
                padding: const EdgeInsets.fromLTRB(4, 0, 4, 8),
                child: Semantics(header: true, child: Text(g.label, style: tkText(size: 13, weight: 700, color: t.muted))),
              ),
              TkSection(
                inset: 14,
                children: [
                  for (final b in g.bookings) _row(t, b, accent),
                ],
              ),
            ],
          ),
        if (all.any(canChange)) Text('Tap a booking to move or cancel it.', style: tkText(size: 13, color: t.muted)),
      ],
    ];
  }

  Widget _row(TkTokens t, Map<String, dynamic> b, ({Color fill, Color ink}) Function(Tone) accent) {
    final c = (b['customer'] as Map?)?.cast<String, dynamic>() ?? const {};
    final phone = c['phone'] as String? ?? '';
    final service = (b['service_name'] as String? ?? '').isEmpty ? 'Booking' : b['service_name'] as String;
    final st = orderStatus(b['status'] as String? ?? '');
    final a = accent(st.tone);
    final open = canChange(b);
    final who = [c['name'] as String? ?? '', b['reference'] as String? ?? ''].where((x) => x.isNotEmpty).join(' · ');
    return MergeSemantics(
      child: Semantics(
        button: open,
        label: open ? 'Move or cancel $service ${b['reference']}' : null,
        child: TkPress(
          onTap: open ? () => _open(b) : null,
          scale: 0.985,
          child: Opacity(
            opacity: b['status'] == 'cancelled' ? 0.6 : 1,
            child: Padding(
              padding: const EdgeInsets.fromLTRB(14, 11, 10, 11),
              child: Row(
                children: [
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Wrap(
                          spacing: 8,
                          crossAxisAlignment: WrapCrossAlignment.center,
                          children: [
                            Text(timeSpan(b), style: tkText(size: 15, weight: 600, color: t.text).copyWith(fontFeatures: tkTabular)),
                            Container(
                              padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
                              decoration: BoxDecoration(color: a.fill.withValues(alpha: 0.16), borderRadius: BorderRadius.circular(999)),
                              child: Text(st.label, style: tkText(size: 11.5, weight: 700, color: a.ink)),
                            ),
                          ],
                        ),
                        Text(service, style: tkText(size: 14, color: t.text)),
                        if (who.isNotEmpty) Text(who, style: tkText(size: 12.5, color: t.muted)),
                      ],
                    ),
                  ),
                  if (phone.isNotEmpty && !phone.contains('•'))
                    TkHeadButton(
                      icon: 'b-phone',
                      label: 'Call ${c['name'] ?? phone}',
                      onCard: true,
                      onTap: () => openOrSay(context, () => callPhone(phone), 'That number could not be called.'),
                    ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }

  Widget _servicesCard(TkTokens t, Map<String, dynamic> sf) {
    final has = (sf['services_table'] as String? ?? '').isNotEmpty;
    return TkPageCard(
      title: 'Services',
      children: [
        if (!has) ...[
          Text('List what customers can book, how long each takes and its price. Taskiem keeps the list for you.', style: tkHint(t)),
          const SizedBox(height: 10),
          Align(
            alignment: Alignment.centerLeft,
            child: TkSmallButton(label: 'Make my services', primary: true, onPressed: _busy ? null : _makeServices),
          ),
        ] else if (_services == null && _servicesError != null)
          TkErrorBox(_servicesError!)
        else if (_services == null)
          Text('Loading your services…', style: tkHint(t))
        else if (_services!.isEmpty)
          Text('No services yet. Add them from Services in the chat.', style: tkHint(t))
        else
          for (final s in _services!)
            TkListRow(
              title: s['name'] as String? ?? '',
              sub: [formatDuration(_int(s['duration_minutes'])), servicePrice(s)].where((x) => x.isNotEmpty).join(' · '),
              icon: 'b-clock',
              square: TkLook.of(context).violet,
            ),
      ],
    );
  }

  Widget _timeBox(TextEditingController c, String label) => SizedBox(
    width: 96,
    child: Semantics(
      label: label,
      child: TkTextBox(controller: c, hint: '09:00', height: 44, size: 16),
    ),
  );

  Widget _hoursCard(TkTokens t) {
    final today = lagosDay(DateTime.now().toUtc().toIso8601String());
    final upcoming = _off.where((d) => d.compareTo(today) >= 0).toList();
    final past = _off.length - upcoming.length;
    return TkPageCard(
      title: 'Booking hours',
      children: [
        Text('In Lagos time. Times are offered in steps of each service’s duration plus its buffer.', style: tkHint(t)),
        const SizedBox(height: 10),
        TkSwitchRow(
          label: 'Same as opening hours',
          hint: 'Take bookings whenever your page shows you open.',
          on: _sameAsHours,
          onChanged: (v) => setState(() => _sameAsHours = v),
        ),
        if (_sameAsHours && !_days.any((d) => d.open)) ...[
          const SizedBox(height: 10),
          Semantics(
            liveRegion: true,
            child: TkErrorBox('Your page has no opening hours, so no times can be booked. Set booking hours here, or opening hours on your page.'),
          ),
        ],
        const SizedBox(height: 10),
        for (var i = 0; i < 7; i++)
          Padding(
            padding: const EdgeInsets.only(bottom: 10),
            child: _sameAsHours
                ? Row(
                    children: [
                      SizedBox(
                        width: 100,
                        child: Text(days[i], style: tkText(size: 15, weight: 600, color: t.text)),
                      ),
                      Expanded(
                        child: Text(_days[i].open ? _days[i].spans.map((s) => '${s.open.text} to ${s.close.text}').join(', ') : 'Closed', style: tkHint(t)),
                      ),
                    ],
                  )
                : Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      TkChoiceRow(
                        radio: false,
                        control: TkCheckbox(checked: _days[i].open),
                        label: Text(days[i], style: tkText(size: 15, weight: 600, color: t.text)),
                        onTap: () => setState(() => _days[i].open = !_days[i].open),
                      ),
                      if (_days[i].open) ...[
                        for (var n = 0; n < _days[i].spans.length; n++)
                          Padding(
                            padding: const EdgeInsets.only(left: 32, bottom: 6),
                            // A row while it fits; at large text, the end time under.
                            child: Wrap(
                              crossAxisAlignment: WrapCrossAlignment.center,
                              runSpacing: 6,
                              children: [
                                _timeBox(_days[i].spans[n].open, '${days[i]} bookings from${_days[i].spans.length > 1 ? ' (period ${n + 1})' : ''}'),
                                Padding(
                                  padding: const EdgeInsets.symmetric(horizontal: 8),
                                  child: Text('to', style: tkText(size: 15, color: t.text)),
                                ),
                                _timeBox(_days[i].spans[n].close, '${days[i]} bookings until${_days[i].spans.length > 1 ? ' (period ${n + 1})' : ''}'),
                                if (_days[i].spans.length > 1)
                                  TkSmallButton(
                                    label: '×',
                                    semantics: 'Remove ${days[i]} period ${n + 1}',
                                    onPressed: () {
                                      final gone = _days[i].spans[n];
                                      setState(() => _days[i].spans.removeAt(n));
                                      WidgetsBinding.instance.addPostFrameCallback((_) {
                                        gone.open.dispose();
                                        gone.close.dispose();
                                      });
                                    },
                                  ),
                              ],
                            ),
                          ),
                        if (_days[i].spans.length < maxOpenings)
                          Padding(
                            padding: const EdgeInsets.only(left: 32),
                            child: Align(
                              alignment: Alignment.centerLeft,
                              child: TkSmallButton(
                                label: '+ Period',
                                semantics: 'Add a booking period on ${days[i]}',
                                onPressed: () => setState(() {
                                  final s = _Span('', '');
                                  s.open.addListener(() => setState(() {}));
                                  s.close.addListener(() => setState(() {}));
                                  _days[i].spans.add(s);
                                }),
                              ),
                            ),
                          ),
                      ] else
                        Padding(
                          padding: const EdgeInsets.only(left: 32),
                          child: Text('No bookings', style: tkHint(t)),
                        ),
                    ],
                  ),
          ),
        if (!_sameAsHours)
          Text('Up to $maxOpenings periods a day, such as 09:00 to 13:00 and 14:00 to 18:00 for a lunch break. Each ends the same day.', style: tkHint(t)),
        const SizedBox(height: 14),
        Semantics(
          header: true,
          child: Text('Days off', style: tkText(size: 15, weight: 600, color: t.text)),
        ),
        Text('Dates with no bookings, such as public holidays.', style: tkHint(t)),
        const SizedBox(height: 6),
        if (upcoming.isEmpty)
          Text('No days off coming up.', style: tkHint(t))
        else
          for (final d in upcoming)
            Row(
              children: [
                Expanded(
                  child: Text('${dayLabel(d)} ${d.substring(0, 4)}', style: tkText(size: 15, color: t.text)),
                ),
                TkSmallButton(
                  label: 'Remove',
                  semantics: 'Remove day off ${dayLabel(d)} ${d.substring(0, 4)}',
                  onPressed: () => setState(() => _off.remove(d)),
                ),
              ],
            ),
        if (past > 0)
          Padding(
            padding: const EdgeInsets.only(top: 6),
            child: Wrap(
              crossAxisAlignment: WrapCrossAlignment.center,
              spacing: 6,
              children: [
                Text('${past == 1 ? '1 past day off is' : '$past past days off are'} kept.', style: tkHint(t)),
                TkLink(label: 'Remove past days off', onTap: () => setState(() => _off.removeWhere((d) => d.compareTo(today) < 0))),
              ],
            ),
          ),
        const SizedBox(height: 10),
        Row(
          crossAxisAlignment: CrossAxisAlignment.end,
          children: [
            Expanded(
              child: TkKitField(
                label: 'Add a day off',
                hint: 'As 2026-12-25',
                child: TkTextBox(controller: _newOff, hint: today),
              ),
            ),
            const SizedBox(width: 8),
            Padding(
              padding: const EdgeInsets.only(bottom: 25),
              child: TkSmallButton(
                label: 'Add day off',
                onPressed:
                    !isDay(_newOff.text.trim()) ||
                        _off.contains(_newOff.text.trim()) ||
                        _newOff.text.trim().compareTo(today) < 0 ||
                        _newOff.text.trim().compareTo(addDays(today, 730)) > 0 ||
                        _off.length >= maxDaysOff
                    ? null
                    : () => setState(() {
                        _off = [..._off, _newOff.text.trim()]..sort();
                        _newOff.clear();
                      }),
              ),
            ),
          ],
        ),
      ],
    );
  }

  Widget _noticeBox(TkTokens t, String label, String hint, Notice n, int defaultMinutes) => Column(
    crossAxisAlignment: CrossAxisAlignment.stretch,
    children: [
      Text(label, style: tkText(size: 12.5, weight: 500, color: t.text)),
      const SizedBox(height: 6),
      Row(
        children: [
          SizedBox(
            width: 110,
            child: Semantics(
              label: '$label, amount',
              child: TkTextBox(controller: n.text, money: true, height: 44, size: 16, hint: n.hours ? '${defaultMinutes ~/ 60}' : '$defaultMinutes'),
            ),
          ),
          const SizedBox(width: 8),
          TkSmallButton(
            label: n.hours ? 'hours' : 'minutes',
            semantics: '$label, unit: ${n.hours ? 'hours' : 'minutes'}',
            onPressed: () async {
              final u = await showTkModalChoices(
                context,
                title: '$label, unit',
                value: n.hours ? 'hours' : 'minutes',
                choices: const [('hours', 'hours'), ('minutes', 'minutes')],
              );
              if (u != null) setState(() => n.hours = u == 'hours');
            },
          ),
        ],
      ),
      const SizedBox(height: 4),
      Text(hint, style: tkHint(t)),
    ],
  );

  Widget _rulesCard(TkTokens t) {
    final problems = _problems;
    final dirty = '${_request()}' != _was;
    return TkPageCard(
      title: 'Booking rules',
      children: [
        TkKitField(
          label: 'How far ahead (days)',
          hint: 'How many days ahead customers may book, up to 180. Empty: $aheadDefault.',
          child: TkTextBox(controller: _ahead, digits: 3, hint: '$aheadDefault'),
        ),
        const SizedBox(height: 14),
        _noticeBox(t, 'How soon', 'The earliest a booking may start, from now. Empty: 2 hours.', _soonest, 120),
        const SizedBox(height: 14),
        _noticeBox(t, 'Change cut-off', 'Customers may move or cancel up to this long before the start. Empty: 2 hours.', _cutoff, 120),
        const SizedBox(height: 14),
        TkSwitchRow(
          label: 'Confirm bookings automatically',
          hint: 'Otherwise each booking waits for you to accept it in WhatsApp or Telegram.',
          on: _autoConfirm,
          onChanged: (v) => setState(() => _autoConfirm = v),
        ),
        for (final p in problems)
          Padding(
            padding: const EdgeInsets.only(top: 8),
            child: Text(p, style: tkText(size: 13, weight: 600, color: t.danger)),
          ),
        if (_settingsNotice.isNotEmpty)
          Padding(
            padding: const EdgeInsets.only(top: 10),
            child: Semantics(
              liveRegion: true,
              child: Text(_settingsNotice, style: tkText(size: 14, color: t.text)),
            ),
          ),
        if (_saveError != null) Padding(padding: const EdgeInsets.only(top: 10), child: TkErrorBox(_saveError!)),
        const SizedBox(height: 12),
        Wrap(
          crossAxisAlignment: WrapCrossAlignment.center,
          spacing: 10,
          children: [
            TkButton(label: _busy ? 'Saving…' : 'Save booking settings', primary: true, enabled: !(_busy || !dirty || problems.isNotEmpty), onPressed: _save),
            if (dirty) Text('Unsaved changes', style: tkHint(t)),
          ],
        ),
      ],
    );
  }
}

/// StorefrontBookings.tsx MoveDialog: a new day and time, in Lagos time.
class _MoveForm extends StatefulWidget {
  const _MoveForm({required this.auth, required this.booking});
  final Auth auth;
  final Map<String, dynamic> booking;

  @override
  State<_MoveForm> createState() => _MoveFormState();
}

class _MoveFormState extends State<_MoveForm> {
  late final _day = TextEditingController(
    text: widget.booking['starts_at'] != null ? lagosDay(widget.booking['starts_at'] as String) : lagosDay(DateTime.now().toUtc().toIso8601String()),
  )..addListener(() => setState(() {}));
  late final _time = TextEditingController(text: widget.booking['starts_at'] != null ? lagosTime(widget.booking['starts_at'] as String) : '')
    ..addListener(() => setState(() {}));
  bool _busy = false;
  String? _error;

  @override
  void dispose() {
    _day.dispose();
    _time.dispose();
    super.dispose();
  }

  /// A date saved on one of the owner's things as the new date (saved
  /// objects §6.4), one picker at a time.
  bool _picking = false;
  Future<void> _dayFromSaved() async {
    if (_picking) return;
    setState(() => _picking = true);
    try {
      final d = await pickSavedDate(context, widget.auth);
      if (d != null && mounted) _day.text = d;
    } finally {
      if (mounted) setState(() => _picking = false);
    }
  }

  /// bookings.ts moveProblem.
  String? get _problem {
    final d = _day.text.trim();
    if (!isDay(d)) return 'Choose the new date.';
    if (d.compareTo(lagosDay(DateTime.now().toUtc().toIso8601String())) < 0) return 'Choose a date from today.';
    return !_hhmm.hasMatch(_time.text.trim()) ? 'Choose the new time.' : null;
  }

  bool get _same {
    final s = widget.booking['starts_at'] as String?;
    return s != null && lagosDay(s) == _day.text.trim() && lagosTime(s) == _time.text.trim();
  }

  Future<void> _move() async {
    setState(() => (_busy = true, _error = null));
    try {
      final r = (await widget.auth.transport.send(
        'POST',
        '/v1/storefront/bookings/${Uri.encodeComponent(widget.booking['reference'] as String)}/move',
        body: {'starts_at': '${_day.text.trim()}T${_time.text.trim()}:00+01:00'},
      ) as Map)['booking'];
      if (mounted) Navigator.of(context).pop((r as Map).cast<String, dynamic>());
    } on Object catch (e) {
      if (mounted) setState(() => (_error = errorWords(e), _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final b = widget.booking;
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text(
          '${b['service_name'] ?? ''}${b['starts_at'] != null ? ', now ${whenLabel(b['starts_at'] as String)}' : ''}. Choose a free time in Lagos time; the customer is texted the new one.',
          style: tkHint(t),
        ),
        const SizedBox(height: 14),
        Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Expanded(
              child: TkKitField(
                label: 'New date',
                child: TkTextBox(controller: _day, hint: '2026-12-01'),
              ),
            ),
            const SizedBox(width: 10),
            SizedBox(
              width: 110,
              child: TkKitField(
                label: 'New time',
                child: TkTextBox(controller: _time, hint: '10:30'),
              ),
            ),
          ],
        ),
        const SizedBox(height: 8),
        Align(
          alignment: Alignment.centerLeft,
          child: SavedButton(label: 'A saved date', semantics: 'Pick a date from your saved things', picking: _picking, busy: _busy, onPressed: _dayFromSaved),
        ),
        if (_date.hasMatch(_day.text.trim()))
          Padding(
            padding: const EdgeInsets.only(top: 6),
            child: Text(
              _problem == 'Choose the new date.' ? 'That is not a date.' : (_problem == 'Choose a date from today.' ? _problem! : dayLabel(_day.text.trim())),
              style: _problem == 'Choose the new time.' || _problem == null ? tkHint(t) : tkText(size: 13, weight: 600, color: t.danger),
            ),
          ),
        if (_error != null) Padding(padding: const EdgeInsets.only(top: 10), child: TkErrorBox(_error!)),
        const SizedBox(height: 14),
        Wrap(
          spacing: 8,
          children: [
            TkButton(label: _busy ? 'Moving…' : 'Move', primary: true, enabled: !(_busy || _problem != null || _same), onPressed: _move),
            TkButton(label: 'Cancel', onPressed: () => Navigator.of(context).pop()),
          ],
        ),
      ],
    );
  }
}


/// One booking (docs/spec/mobile-app-look.md, "The remaining screens"):
/// the customer with Call and Text; Move to a free time, picked by day and
/// time; or Cancel booking. It closes with what to say.
class BookingSheet extends StatefulWidget {
  const BookingSheet({super.key, required this.auth, required this.booking});
  final Auth auth;
  final Map<String, dynamic> booking;

  @override
  State<BookingSheet> createState() => _BookingSheetState();
}

class _BookingSheetState extends State<BookingSheet> {
  List<String>? _days;
  String _day = '';
  List<String>? _times;
  String _time = '';
  String? _error;
  bool _busy = false;
  bool _cantRead = false;

  Map<String, dynamic> get _b => widget.booking;
  String get _ref => _b['reference'] as String? ?? '';

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load([String day = '']) async {
    setState(() => (_times = null, _time = ''));
    try {
      final r = (await widget.auth.transport.send(
        'GET',
        '/v1/storefront/bookings/${Uri.encodeComponent(_ref)}/times',
        query: {if (day.isNotEmpty) 'day': day},
      ) as Map).cast<String, dynamic>();
      if (!mounted) return;
      setState(() {
        _days = [for (final d in (r['days'] as List? ?? const [])) '$d'];
        _day = r['day'] as String? ?? '';
        _times = [for (final t in (r['times'] as List? ?? const [])) '$t'];
      });
    } on Object catch (e) {
      if (mounted) setState(() => (_error = errorWords(e), _cantRead = true, _days = const [], _times = const []));
    }
  }

  Future<void> _move() async {
    if (_time.isEmpty) return;
    setState(() => (_busy = true, _error = null));
    try {
      final r = (await widget.auth.transport.send('POST', '/v1/storefront/bookings/${Uri.encodeComponent(_ref)}/move', body: {'starts_at': _time}) as Map)['booking'] as Map;
      if (!mounted) return;
      Navigator.of(context).pop('${r['reference']} is moved to ${r['starts_at'] != null ? whenLabel(r['starts_at'] as String) : 'its new time'}; the customer is told.');
    } on Object catch (e) {
      if (mounted) setState(() => (_error = errorWords(e), _busy = false));
    }
  }

  Future<void> _cancel() async {
    if (!await askCancelBooking(context, _b) || !mounted) return;
    setState(() => (_busy = true, _error = null));
    try {
      final said = await cancelBooking(widget.auth, _b);
      if (mounted) Navigator.of(context).pop(said);
    } on Object catch (e) {
      if (mounted) setState(() => (_error = errorWords(e), _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final c = (_b['customer'] as Map?)?.cast<String, dynamic>() ?? const {};
    final name = c['name'] as String? ?? '';
    final phone = c['phone'] as String? ?? '';
    final shown = phone.isNotEmpty && !phone.contains('•');
    final service = (_b['service_name'] as String? ?? '').isEmpty ? 'Booking' : _b['service_name'] as String;
    Widget chip(String words, bool on, VoidCallback tap, {String? label}) => TapArea(
      child: Semantics(
        button: true,
        selected: on,
        label: label ?? words,
        excludeSemantics: true,
        onTap: tap,
        child: GestureDetector(
          onTap: tap,
          child: Container(
            padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
            decoration: BoxDecoration(color: on ? (TkTheme.of(context).mode == TkMode.dark ? look.lime : t.brand) : look.card, borderRadius: BorderRadius.circular(999)),
            child: Text(words, style: tkText(size: 14, weight: 600, color: on ? (TkTheme.of(context).mode == TkMode.dark ? t.brand : const Color(0xFFFFFFFF)) : t.text)),
          ),
        ),
      ),
    );
    return TkFormSheet(
      title: 'Booking $_ref',
      crumb: '$service${_b['starts_at'] != null ? ' · ${whenLabel(_b['starts_at'] as String)}' : ''}',
      action: _busy ? 'Moving…' : 'Move',
      onAction: _busy || _time.isEmpty ? null : _move,
      second: 'Cancel booking',
      secondDanger: true,
      onSecond: _busy ? null : _cancel,
      children: [
        // The customer, with Call and Text as round icons beside them.
        TkPersonCard(
          name: name.isEmpty ? 'The customer' : name,
          sub: shown ? groupedPhone(phone) : phone,
          actions: const [],
          trailing: !shown
              ? null
              : Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    TkHeadButton(icon: 'b-phone', label: 'Call $phone', onCard: true, onTap: () => openOrSay(context, () => callPhone(phone), 'That number could not be called.')),
                    const SizedBox(width: 8),
                    TkHeadButton(icon: 'b-chat', label: 'Text $phone', onCard: true, onTap: () => openOrSay(context, () => textPhone(phone), 'That number could not be texted.')),
                  ],
                ),
        ),
        Text('MOVE TO', semanticsLabel: 'Move to', style: tkText(size: 11.5, weight: 700, letterSpacing: 0.4, color: t.muted)),
        if (_days == null)
          const TkLoading(rows: 1, label: 'Loading free times…')
        else if (_days!.isEmpty && !_cantRead)
          Text('No free time to move it to in the days you take bookings.', style: tkHint(t))
        else ...[
          Wrap(spacing: 8, runSpacing: 8, children: [for (final d in _days!) chip(dayLabel(d), d == _day, () => d == _day ? null : _load(d))]),
          if (_times == null)
            const TkLoading(rows: 1, label: 'Loading free times…')
          else
            Wrap(
              spacing: 8,
              runSpacing: 8,
              children: [for (final x in _times!) chip(lagosTime(x), x == _time, () => setState(() => _time = x), label: '${dayLabel(_day)}, ${lagosTime(x)}')],
            ),
          if (!_cantRead) Text('Free times only · the customer is texted the new time', style: tkText(size: 12.5, color: t.muted)),
        ],
        if (_error != null) TkErrorBox(_error!),
        if (_cantRead)
          Align(
            alignment: Alignment.centerLeft,
            child: TkLink(
              label: 'Type a day and time instead',
              onTap: () async {
                final said = await typeMove(context, widget.auth, _b);
                if (said != null && context.mounted) Navigator.of(context).pop(said);
              },
            ),
          ),
      ],
    );
  }
}
