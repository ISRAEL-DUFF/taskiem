// Book in a business's conversation (M6 part B; web/src/pages/ChatBook.tsx,
// lib/linkbook.ts, lib/bookings.ts; business-chat-tools.md §3): their
// services as their storefront shows them, a day and a time from their
// availability, and a booking through their storefront's booking. Pick a
// service, then a time, then book.

import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/transport.dart';
import '../app/auth.dart';
import '../app/session.dart' show uuid4;
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../kit/list.dart';
import '../kit/sheet.dart';
import '../kit/tap_area.dart';
import '../theme/theme.dart';
import 'approve.dart' show TkTextBox;
import 'chats.dart' show ChatAvatar;
import 'words.dart' show naira;

List<Map<String, dynamic>> _maps(Object? v) => (v as List? ?? const []).whereType<Map<String, dynamic>>().toList();
int? _int(Object? v) => (v as num?)?.toInt();

/// Lagos keeps one hour ahead of UTC all year.
DateTime _lagos(DateTime t) => t.toUtc().add(const Duration(hours: 1));

const _days = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'];
const _months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

/// bookings.ts lagosTime: "10:30".
String lagosTime(String iso) {
  final t = DateTime.tryParse(iso);
  if (t == null) return iso;
  final l = _lagos(t);
  return '${l.hour.toString().padLeft(2, '0')}:${l.minute.toString().padLeft(2, '0')}';
}

/// bookings.ts lagosDay: "2026-10-24".
String lagosDay(String iso) {
  final t = DateTime.tryParse(iso);
  if (t == null) return iso;
  final l = _lagos(t);
  return '${l.year}-${l.month.toString().padLeft(2, '0')}-${l.day.toString().padLeft(2, '0')}';
}

/// bookings.ts dayLabel: "2026-10-24" as "Sat 24 Oct".
String dayLabel(String day) {
  final d = DateTime.tryParse('${day}T12:00:00Z');
  if (d == null) return day;
  return '${_days[d.weekday - 1]} ${d.day} ${_months[d.month - 1]}';
}

/// bookings.ts whenLabel: "Sat 24 Oct, 10:30".
String whenLabel(String iso) => '${dayLabel(lagosDay(iso))}, ${lagosTime(iso)}';

/// bookings.ts formatDuration: "1 hour 30 min".
String formatDuration(int? minutes) {
  if (minutes == null || minutes <= 0) return '';
  final h = minutes ~/ 60;
  final m = minutes % 60;
  final hours = h == 0 ? '' : (h == 1 ? '1 hour' : '$h hours');
  return [hours, if (m != 0) '$m min'].where((x) => x.isNotEmpty).join(' ');
}

/// bookings.ts servicePrice.
String servicePrice(Map<String, dynamic> s) => _int(s['price_kobo']) == null ? 'Price on the day' : naira(_int(s['price_kobo'])!);

String _serviceLine(Map<String, dynamic> s) => [formatDuration(_int(s['minutes'])), servicePrice(s)].where((x) => x.isNotEmpty).join(' · ');

/// linkbook.ts bookLine: the tile's line.
String bookLine(Map<String, dynamic> s) {
  if (s['open'] != true) return 'Not taking bookings online';
  final n = _maps(s['services']).length;
  return '$n ${n == 1 ? 'service' : 'services'} · choose a time';
}

/// linkbook.ts bookPayWords.
String bookPayWords(String business) => 'You pay $business at the appointment, or as you agree with them.';

/// linkbook.ts linkServices: their services, read once a minute at most:
/// the panel's tile and the sheet share the answer.
final _cache = <String, ({DateTime at, Future<Map<String, dynamic>> got})>{};
Future<Map<String, dynamic>> linkServices(Transport t, String link) {
  final hit = _cache[link];
  if (hit != null && DateTime.now().difference(hit.at) < const Duration(minutes: 1)) return hit.got;
  final got = t.send('GET', '/v1/link-chats/${Uri.encodeComponent(link)}/services').then((r) => (r as Map).cast<String, dynamic>());
  got.catchError((Object _) {
    _cache.remove(link);
    return <String, dynamic>{};
  });
  _cache[link] = (at: DateTime.now(), got: got);
  return got;
}

/// Opens Book; the future ends with what was booked, said.
Future<String?> showBookSheet(BuildContext context, {required Auth auth, required String link, required String business}) =>
    showTkSheet<String>(context, (_) => BookSheet(auth: auth, link: link, business: business));

class BookSheet extends StatefulWidget {
  const BookSheet({super.key, required this.auth, required this.link, required this.business});
  final Auth auth;
  final String link;
  final String business;

  @override
  State<BookSheet> createState() => _BookSheetState();
}

enum _Step { service, time, confirm }

class _BookSheetState extends State<BookSheet> {
  Map<String, dynamic>? _data;
  String? _loadError;
  Map<String, dynamic>? _service;
  String? _day;
  String? _start;
  _Step _step = _Step.service;
  Map<String, dynamic>? _times;
  String? _timesError;
  final _note = TextEditingController();
  TextEditingController? _phone;
  bool _busy = false;
  String? _error;
  bool _taken = false;
  String? _done;
  final _keys = <String, String>{};

  Transport get _t => widget.auth.transport;
  String get _biz => widget.business;
  bool get _mayBook => widget.auth.can('chat.trade');

  @override
  void initState() {
    super.initState();
    linkServices(_t, widget.link).then((d) {
      if (!mounted) return;
      setState(() {
        _data = d;
        _phone = TextEditingController(text: d['phone'] as String? ?? '')..addListener(() => setState(() {}));
      });
    }, onError: (Object e) {
      if (mounted) setState(() => _loadError = e is ApiError ? e.message : 'You’re offline. Try again when you’re back online.');
    });
  }

  @override
  void dispose() {
    _note.dispose();
    _phone?.dispose();
    super.dispose();
  }

  Future<void> _loadTimes() async {
    final s = _service;
    if (s == null) return;
    setState(() => (_times = null, _timesError = null));
    try {
      final r = await _t.send(
        'GET',
        '/v1/link-chats/${Uri.encodeComponent(widget.link)}/services/${Uri.encodeComponent(s['id'] as String)}/times',
        query: {'day': ?_day},
      );
      if (mounted) setState(() => _times = (r as Map).cast<String, dynamic>());
    } on ApiError catch (e) {
      if (mounted) setState(() => _timesError = e.message);
    } on Offline {
      if (mounted) setState(() => _timesError = 'You’re offline. Try again when you’re back online.');
    }
  }

  void _pick(Map<String, dynamic> s) {
    setState(() {
      _service = s;
      _day = null;
      _start = null;
      _taken = false;
      _step = _Step.time;
    });
    _loadTimes();
  }

  Future<void> _book() async {
    final s = _service;
    final start = _start;
    if (s == null || start == null) return;
    setState(() => (_busy = true, _error = null, _taken = false));
    final key = _keys['${s['id']} $start'] ??= 'book-${uuid4()}';
    try {
      final out = await _t.send(
        'POST',
        '/v1/link-chats/${Uri.encodeComponent(widget.link)}/bookings',
        body: {'service_id': s['id'], 'start': start, 'note': _note.text.trim(), 'phone': _phone?.text.trim() ?? ''},
        headers: {'Idempotency-Key': key},
      ) as Map<String, dynamic>;
      final b = (out['booking'] as Map).cast<String, dynamic>();
      final words = 'Booked with $_biz: ${b['service_name']}, ${whenLabel(b['starts_at'] as String)}';
      if (!mounted) return;
      setState(() => _done = words);
      Timer(const Duration(milliseconds: 1300), () {
        if (mounted) Navigator.of(context).maybePop(words);
      });
    } on ApiError catch (e) {
      if (!mounted) return;
      if (e.body['code'] == 'slot_taken') {
        // Taken since: fresh times for the day, to choose another.
        setState(() {
          _busy = false;
          _taken = true;
          _start = null;
          _step = _Step.time;
        });
        _loadTimes();
        return;
      }
      setState(() => (_error = e.message, _busy = false));
    } on Offline {
      if (mounted) setState(() => (_error = 'You’re offline. Try again when you’re back online.', _busy = false));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final s = _service;
    final title = _step == _Step.confirm ? 'Check your booking' : (_step == _Step.time && s != null ? s['name'] as String? ?? '' : 'Book with $_biz');
    return TkSheet(
      title: title,
      onBack: switch (_step) {
        _Step.confirm => () => setState(() => _step = _Step.time),
        _Step.time => () => setState(() => _step = _Step.service),
        _Step.service => null,
      },
      crumb: _step == _Step.service ? null : 'Book with $_biz',
      lead: _step == _Step.service ? ChatAvatar(name: _biz, size: 48, letterSize: 16) : null,
      sub: _step == _Step.service ? 'Their services, as their storefront shows them' : (_step == _Step.time && s != null ? _serviceLine(s) : null),
      overlay: _done == null ? null : TkSavedTick(words: _done!),
      footer: _step == _Step.time && _start != null
          ? TkCta(
              label: '${dayLabel(_times?['day'] as String? ?? '')} at ${lagosTime(_start!)}',
              icon: 'arrow-right',
              iconAfter: true,
              onTap: () => setState(() => _step = _Step.confirm),
            )
          : _step == _Step.confirm && _mayBook
          ? TkCta(label: _busy ? 'Booking…' : 'Book', onTap: _busy || (_phone?.text.trim() ?? '').isEmpty ? null : _book)
          : null,
      body: _body(t),
    );
  }

  Widget _body(TkTokens t) {
    final data = _data;
    if (data == null) {
      return ListView(
        children: [
          if (_loadError != null) Padding(padding: const EdgeInsets.symmetric(horizontal: 20), child: TkErrorBox(_loadError!)) else const TkLoading(label: 'Loading their services…'),
        ],
      );
    }
    final services = _maps(data['services']);
    if (services.isEmpty) {
      return ListView(children: [TkEmpty(icon: 'calendar', title: '$_biz has no services to book', line: 'Ask them in the chat.')]);
    }
    final open = data['open'] == true;
    switch (_step) {
      case _Step.service:
        return ListView(
          padding: const EdgeInsets.symmetric(horizontal: 12),
          children: [
            if (!open) Padding(padding: const EdgeInsets.fromLTRB(8, 4, 8, 12), child: Text(data['closed'] as String? ?? '', style: tkFormNow(t))),
            for (final s in services)
              TkRow(
                name: s['name'] as String? ?? '',
                sub: [_serviceLine(s), if ((s['description'] as String? ?? '').isNotEmpty) s['description'] as String].join('\n'),
                icon: 'calendar',
                trailing: open ? TkIcon('chevron', size: 18, color: t.muted) : null,
                onTap: open ? () => _pick(s) : null,
              ),
          ],
        );
      case _Step.time:
        final times = _times;
        return ListView(
          padding: const EdgeInsets.symmetric(horizontal: 12),
          children: [
            if (_taken)
              Semantics(
                liveRegion: true,
                child: Padding(
                  padding: const EdgeInsets.fromLTRB(8, 0, 8, 8),
                  child: Text('That time was just taken. Please choose another.', style: tkText(size: 13, weight: 600, color: t.danger)),
                ),
              ),
            if (times == null)
              _timesError != null ? TkErrorBox(_timesError!) : const TkLoading(label: 'Loading free times…')
            else if (((times['days'] as List?) ?? const []).isEmpty)
              TkEmpty(
                icon: 'calendar',
                title: 'No free times',
                line: '$_biz has no free time for this in the days they take bookings. Ask them in the chat.',
              )
            else ...[
              _head(t, 'Day'),
              _Chips(
                label: 'Day',
                values: [for (final d in ((times['days'] as List?) ?? const []).whereType<String>()) (d, dayLabel(d))],
                on: times['day'] as String?,
                onPick: (d) {
                  setState(() => (_day = d, _start = null));
                  _loadTimes();
                },
              ),
              _head(t, 'Time'),
              if (((times['times'] as List?) ?? const []).isEmpty)
                Padding(padding: const EdgeInsets.symmetric(horizontal: 8), child: Text('No free time that day. Please choose another.', style: tkFormNow(t)))
              else
                _Chips(
                  label: 'Time',
                  grid: true,
                  values: [for (final x in ((times['times'] as List?) ?? const []).whereType<String>()) (x, lagosTime(x))],
                  on: _start,
                  onPick: (x) => setState(() => _start = x),
                ),
            ],
          ],
        );
      case _Step.confirm:
        final s = _service!;
        return TkFormBody(
          children: [
            TkLines([(s['name'] as String? ?? '', servicePrice(s)), ('When', whenLabel(_start!)), ('How long', formatDuration(_int(s['minutes'])))]),
            Text(bookPayWords(_biz), style: tkFormNow(t)),
            if (_mayBook) ...[
              TkKitField(
                label: 'Phone number they can reach you on',
                hint: 'Given to $_biz with the booking.',
                child: TkTextBox(controller: _phone!, phone: true, hint: '0803 123 4567'),
              ),
              TkKitField(label: 'Note for them', hint: 'Optional', child: TkTextBox(controller: _note, lines: 2)),
            ] else
              Text('Your role cannot book with other businesses. Ask the owner.', style: tkFormNow(t)),
            if (_error != null) TkErrorBox(_error!),
          ],
        );
    }
  }

  /// `.tk-book-head`: 8 px round, 14 px bold, muted.
  Widget _head(TkTokens t, String words) => Padding(
    padding: const EdgeInsets.all(8),
    child: Semantics(header: true, child: Text(words, style: tkText(size: 14, weight: 700, color: t.muted))),
  );
}

/// `.tk-book-times .tk-chips`: the days in a row; the times in a grid of
/// boxes at least 84 wide; the one chosen in pine.
class _Chips extends StatelessWidget {
  const _Chips({required this.label, required this.values, required this.on, required this.onPick, this.grid = false});
  final String label;
  final List<(String, String)> values;
  final String? on;
  final ValueChanged<String> onPick;
  final bool grid;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final dark = TkTheme.of(context).mode == TkMode.dark;
    Widget chip((String, String) v) {
      final pressed = v.$1 == on;
      final (bg, ink) = pressed ? (dark ? (t.brandDark, t.brandDarkText) : (t.brand, const Color(0xFFFFFFFF))) : (t.tkSoft, t.text);
      return TapArea(
        child: Semantics(
          button: true,
          selected: pressed,
          label: v.$2,
          onTap: () => onPick(v.$1),
          excludeSemantics: true,
          child: GestureDetector(
            onTap: () => onPick(v.$1),
            child: Container(
              constraints: const BoxConstraints(minHeight: 36),
              padding: const EdgeInsets.symmetric(horizontal: 14),
              alignment: Alignment.center,
              decoration: BoxDecoration(color: bg, borderRadius: BorderRadius.circular(999)),
              child: Text(v.$2, style: tkText(size: 14, weight: 600, color: ink)),
            ),
          ),
        ),
      );
    }

    return Semantics(
      container: true,
      label: label,
      explicitChildNodes: true,
      child: Padding(
        padding: const EdgeInsets.fromLTRB(8, 0, 8, 10),
        child: grid
            ? LayoutBuilder(
                builder: (context, box) {
                  final cols = ((box.maxWidth + 8) / (84 + 8)).floor().clamp(1, 99);
                  final w = (box.maxWidth - 8 * (cols - 1)) / cols;
                  return Wrap(spacing: 8, runSpacing: 8, children: [for (final v in values) SizedBox(width: w, child: chip(v))]);
                },
              )
            : SingleChildScrollView(
                scrollDirection: Axis.horizontal,
                child: Row(children: [for (var i = 0; i < values.length; i++) ...[if (i > 0) const SizedBox(width: 8), chip(values[i])]]),
              ),
      ),
    );
  }
}
