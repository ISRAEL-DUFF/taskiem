// Search in a conversation (web/src/pages/ChatSearch.tsx, DayPicker.tsx;
// styles.css .chat-search, .chat-search-row, .search-result, .day-picker),
// as WhatsApp's, the whole screen on a phone: its own head, a box, a
// calendar to go to a day, and the messages found as rows (who, when, and
// the words around the hit, marked). A row, or a day, takes the
// conversation to that message.

import 'dart:async';

import 'package:flutter/material.dart' show InputDecoration, Material, MaterialType, TextField;
import 'package:flutter/services.dart' show TextInputAction;
import 'package:flutter/widgets.dart';

import '../api/api.g.dart';
import '../api/transport.dart';
import '../kit/icon.dart';
import '../kit/kit.dart';
import '../theme/theme.dart';
import 'words.dart';

/// Opens the search over the conversation: `.chat-info`'s `slide-side`
/// (in from 16 px to the right, 0.2 s), gone at once when closed.
Future<void> showChatSearch(
  BuildContext context, {
  required String name,
  required Future<List<LinkMessage>> Function(String q) search,
  required ValueChanged<String> onJump,
  required Future<String?> Function(String day) firstOn,
  required DateTime Function() now,
}) {
  final still = MediaQuery.maybeDisableAnimationsOf(context) ?? false;
  return Navigator.of(context).push<void>(
    PageRouteBuilder<void>(
      transitionDuration: still ? Duration.zero : const Duration(milliseconds: 200),
      reverseTransitionDuration: Duration.zero,
      pageBuilder: (context, _, _) => ChatSearchScreen(name: name, search: search, onJump: onJump, firstOn: firstOn, now: now),
      transitionsBuilder: (context, a, _, child) {
        final e = CurvedAnimation(parent: a, curve: Curves.easeOut);
        return FadeTransition(
          opacity: e,
          child: AnimatedBuilder(
            animation: e,
            builder: (context, child) => Transform.translate(offset: Offset(16 * (1 - e.value), 0), child: child),
            child: child,
          ),
        );
      },
    ),
  );
}

class ChatSearchScreen extends StatefulWidget {
  const ChatSearchScreen({super.key, required this.name, required this.search, required this.onJump, required this.firstOn, required this.now});

  /// The business the conversation is with.
  final String name;

  /// The messages that say [q] (GET …/messages?q=).
  final Future<List<LinkMessage>> Function(String q) search;

  /// Go to a message of the thread (the screen has closed).
  final ValueChanged<String> onJump;

  /// The first message on the day or after it, if any.
  final Future<String?> Function(String day) firstOn;
  final DateTime Function() now;

  @override
  State<ChatSearchScreen> createState() => _ChatSearchScreenState();
}

class _ChatSearchScreenState extends State<ChatSearchScreen> {
  final _q = TextEditingController();
  Timer? _soon;
  List<LinkMessage>? _found;
  String? _error;
  bool _dateOpen = false;
  String _note = '';

  String get _query => _q.text.trim();

  @override
  void initState() {
    super.initState();
    _q.addListener(_typed);
  }

  @override
  void dispose() {
    _soon?.cancel();
    _q.dispose();
    super.dispose();
  }

  /// Searched a moment after the typing stops; an older answer never
  /// replaces a newer.
  void _typed() {
    _soon?.cancel();
    final q = _query;
    setState(() {
      _error = null;
      if (q.length < 2) _found = null;
    });
    if (q.length < 2) return;
    _soon = Timer(const Duration(milliseconds: 250), () {
      widget.search(q).then((r) {
        if (mounted && _query == q) setState(() => _found = r);
      }, onError: (Object e) {
        if (mounted && _query == q) setState(() => _error = _say(e));
      });
    });
  }

  String _say(Object e) => e is ApiError ? e.message : 'You’re offline. Try again when you’re back online.';

  void _jump(String id) {
    Navigator.of(context).pop();
    widget.onJump(id);
  }

  Future<void> _day(DateTime d) async {
    setState(() => _dateOpen = false);
    try {
      final id = await widget.firstOn(dayKey(d));
      if (!mounted) return;
      if (id == null) {
        setState(() => _note = 'No messages on or after that day.');
      } else {
        _jump(id);
      }
    } on Object catch (e) {
      if (mounted) setState(() => _error = _say(e));
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final top = MediaQuery.paddingOf(context).top;
    final width = MediaQuery.sizeOf(context).width;
    final q = _query;
    Widget hint(String s) => Padding(
      // `.chat-search-empty { padding: 28px 24px; font-size: 14.5px }`
      padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 28),
      child: Semantics(
        liveRegion: true,
        child: Text(s, textAlign: TextAlign.center, style: tkText(size: 14.5, color: t.muted)),
      ),
    );
    final found = _found;
    final body = q.length < 2
        ? hint('Search for messages within ${widget.name}.')
        : found == null
        ? hint('Searching…')
        : found.isEmpty
        ? hint('No messages say that.')
        : Semantics(
            container: true,
            label: '${found.length} ${found.length == 1 ? 'message' : 'messages'} found',
            explicitChildNodes: true,
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [for (final m in found) _Result(m: m, name: widget.name, query: q, now: widget.now(), onTap: () => _jump(m.id))],
            ),
          );
    return PopScope(
      // Back closes the calendar first, then the search.
      canPop: !_dateOpen,
      onPopInvokedWithResult: (popped, _) {
        if (!popped && _dateOpen) setState(() => _dateOpen = false);
      },
      child: ColoredBox(
        color: t.panel,
        child: Semantics(
          container: true,
          scopesRoute: true,
          namesRoute: true,
          explicitChildNodes: true,
          label: 'Search messages',
          child: Stack(
            children: [
              Positioned.fill(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    SizedBox(height: top),
                    // `.chat-info-head`: 10 px 14 px, a line under it.
                    Container(
                      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
                      decoration: BoxDecoration(border: Border(bottom: BorderSide(color: t.border))),
                      child: Row(
                        children: [
                          _IconButton(label: 'Close search', icon: 'close', width: 36, onTap: () => Navigator.of(context).pop()),
                          const SizedBox(width: 12),
                          Expanded(
                            child: Semantics(
                              header: true,
                              child: Text('Search messages', style: tkText(size: 16, weight: 600, color: t.text)),
                            ),
                          ),
                        ],
                      ),
                    ),
                    // `.chat-search-row { padding: 10px 12px 6px 6px; gap: 6px }`
                    Padding(
                      padding: const EdgeInsets.fromLTRB(6, 10, 12, 6),
                      child: Row(
                        children: [
                          _IconButton(
                            label: 'Search by date',
                            icon: 'calendar',
                            width: 40,
                            on: _dateOpen,
                            onTap: () => setState(() {
                              _dateOpen = !_dateOpen;
                              _note = '';
                            }),
                          ),
                          const SizedBox(width: 6),
                          Expanded(child: _Box(controller: _q, name: widget.name)),
                        ],
                      ),
                    ),
                    if (_note.isNotEmpty)
                      Padding(
                        padding: const EdgeInsets.fromLTRB(56, 0, 16, 6),
                        child: Semantics(liveRegion: true, child: Text(_note, style: tkHint(t))),
                      ),
                    if (_error != null) Padding(padding: const EdgeInsets.symmetric(horizontal: 12), child: TkErrorBox(_error!)),
                    Expanded(child: SingleChildScrollView(child: body)),
                  ],
                ),
              ),
              if (_dateOpen) ...[
                // A tap elsewhere closes it.
                Positioned.fill(
                  child: GestureDetector(behavior: HitTestBehavior.opaque, onTap: () => setState(() => _dateOpen = false)),
                ),
                Positioned(
                  left: 6,
                  // Under the calendar's button, 6 px down.
                  top: top + 61 + 10 + 41 + 6,
                  width: (width - 24).clamp(0.0, 330.0),
                  child: _DayPicker(today: startOfDay(widget.now().toLocal()), onPick: _day),
                ),
              ],
            ],
          ),
        ),
      ),
    );
  }
}

/// `.chat-search-row .icon-button`, `.chat-info-head .icon-button`: no box
/// of its own, the chat's grey while its calendar is open.
class _IconButton extends StatelessWidget {
  const _IconButton({required this.label, required this.icon, required this.width, required this.onTap, this.on = false});
  final String label;
  final String icon;
  final double width;
  final VoidCallback onTap;
  final bool on;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Semantics(
      button: true,
      label: label,
      expanded: icon == 'calendar' ? on : null,
      excludeSemantics: true,
      child: TkPress(
        onTap: onTap,
        child: Container(
          width: width,
          height: 40,
          alignment: Alignment.center,
          decoration: BoxDecoration(color: on ? t.chatSelect : null, borderRadius: BorderRadius.circular(8)),
          child: TkIcon(icon, size: 18, color: t.text),
        ),
      ),
    );
  }
}

/// `.chat-search-row .search input`: the pill, 42 px, its glass inside.
class _Box extends StatelessWidget {
  const _Box({required this.controller, required this.name});
  final TextEditingController controller;
  final String name;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Semantics(
      label: 'Search messages in $name',
      textField: true,
      child: Container(
        height: 42,
        decoration: BoxDecoration(color: t.field, borderRadius: BorderRadius.circular(999)),
        padding: const EdgeInsets.only(left: 12, right: 12),
        child: Row(
          children: [
            TkIcon('search', size: 16, color: t.muted),
            const SizedBox(width: 8),
            Expanded(
              child: Material(
                type: MaterialType.transparency,
                child: TextField(
                  controller: controller,
                  textInputAction: TextInputAction.search,
                  cursorColor: t.text,
                  showCursor: !TkField.debugHideCaret,
                  style: tkText(size: 15, color: t.text),
                  decoration: InputDecoration.collapsed(
                    hintText: 'Search',
                    hintStyle: tkText(size: 15, color: t.muted.withValues(alpha: t.muted.a * 0.72)),
                  ),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// `.search-result`: who and when, then the words around the hit, the
/// found words bold, three lines at most.
class _Result extends StatelessWidget {
  const _Result({required this.m, required this.name, required this.query, required this.now, required this.onTap});
  final LinkMessage m;
  final String name;
  final String query;
  final DateTime now;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final who = m.mine ? 'You' : (m.senderName ?? name);
    final words = around(m.body ?? previewLine(m, name), query);
    return Semantics(
      button: true,
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: onTap,
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
          decoration: BoxDecoration(
            border: Border(bottom: BorderSide(color: cssMix(t.border, const Color(0x00000000), 0.6))),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Row(
                children: [
                  Expanded(
                    child: Text(
                      who,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: tkText(size: 13, weight: 600, lineHeight: 1.2, color: t.text),
                    ),
                  ),
                  const SizedBox(width: 12),
                  Text(listTime(m.createdAt, now), style: tkText(size: 13, lineHeight: 1.2, color: t.muted)),
                ],
              ),
              const SizedBox(height: 3),
              Text.rich(
                TextSpan(
                  children: [
                    for (final p in highlight(words, query))
                      TextSpan(
                        text: p.text,
                        style: p.hit
                            ? TextStyle(color: t.text, fontWeight: FontWeight.w700, fontVariations: const [FontVariation('wght', 700)])
                            : null,
                      ),
                  ],
                ),
                maxLines: 3,
                overflow: TextOverflow.ellipsis,
                style: tkText(size: 14.5, lineHeight: 1.35, color: t.muted),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

/// DayPicker.tsx: the month with its arrows, the week's days, round day
/// buttons, today filled in, the days after today dimmed and not to be
/// chosen.
class _DayPicker extends StatefulWidget {
  const _DayPicker({required this.today, required this.onPick});
  final DateTime today;
  final ValueChanged<DateTime> onPick;

  @override
  State<_DayPicker> createState() => _DayPickerState();
}

class _DayPickerState extends State<_DayPicker> {
  late DateTime _focus = widget.today;

  void _go(DateTime to) => setState(() => _focus = to.isAfter(widget.today) ? widget.today : to);

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final today = widget.today;
    final weeks = monthGrid(_focus.year, _focus.month);
    final month = monthLabel(_focus);
    final canNext = !sameMonth(_focus, today) && _focus.isBefore(today);
    Widget arrow(String label, String icon, bool enabled, VoidCallback onTap) => Semantics(
      button: true,
      enabled: enabled,
      label: label,
      excludeSemantics: true,
      child: GestureDetector(
        onTap: enabled ? onTap : null,
        child: SizedBox(
          width: 36,
          height: 40,
          child: Center(
            child: Opacity(opacity: enabled ? 1 : 0.4, child: TkIcon(icon, size: 18, color: enabled ? t.text : t.muted)),
          ),
        ),
      ),
    );
    return Semantics(
      container: true,
      label: 'Go to a day',
      explicitChildNodes: true,
      child: Container(
        // `.day-picker { padding: 10px 12px 14px; border-radius: 14px }`
        padding: const EdgeInsets.fromLTRB(12, 10, 12, 14),
        decoration: BoxDecoration(
          color: t.panel,
          borderRadius: BorderRadius.circular(14),
          border: Border.all(color: t.border),
          boxShadow: [BoxShadow(color: t.chatShadow, offset: const Offset(0, 4), blurRadius: 18)],
        ),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            // `.dp-head { padding: 4px 2px 8px; gap: 2px }`
            Padding(
              padding: const EdgeInsets.fromLTRB(2, 4, 2, 8),
              child: Row(
                children: [
                  Expanded(
                    child: Semantics(liveRegion: true, child: Text(month, style: tkText(size: 16, weight: 600, color: t.text))),
                  ),
                  arrow('Previous month', 'back', true, () => _go(addMonths(_focus, -1))),
                  const SizedBox(width: 2),
                  arrow('Next month', 'chevron', canNext, () => _go(addMonths(_focus, 1))),
                ],
              ),
            ),
            // `.dp-grid th`: 32 px, 6 px under the words, a line.
            Container(
              height: 32,
              decoration: BoxDecoration(border: Border(bottom: BorderSide(color: t.border))),
              padding: const EdgeInsets.only(bottom: 6),
              child: Row(
                children: [
                  for (final w in weekdays)
                    Expanded(
                      child: Semantics(
                        label: w,
                        excludeSemantics: true,
                        child: Center(
                          child: Text(w.substring(0, 3), style: tkText(size: 13, weight: 500, color: t.muted)),
                        ),
                      ),
                    ),
                ],
              ),
            ),
            for (final w in weeks)
              SizedBox(
                height: 42.08,
                child: Row(
                  children: [
                    for (final d in w)
                      Expanded(
                        // `.dp-grid td { padding: 2px 0 }`, the button 2.5 px down.
                        child: Align(
                          alignment: Alignment.topCenter,
                          child: Padding(
                            padding: const EdgeInsets.only(top: 2.5),
                            child: _Day(
                            d: d,
                            today: sameDay(d, today),
                            other: !sameMonth(d, _focus),
                            future: d.isAfter(today),
                            onTap: () => widget.onPick(d),
                            ),
                          ),
                        ),
                      ),
                  ],
                ),
              ),
          ],
        ),
      ),
    );
  }
}

/// `.dp-day`: 38 px round; today filled in the accent; another month's
/// muted; after today dimmed and not to be chosen.
class _Day extends StatelessWidget {
  const _Day({required this.d, required this.today, required this.other, required this.future, required this.onTap});
  final DateTime d;
  final bool today;
  final bool other;
  final bool future;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final ink = today ? t.accentText : (other || future ? t.muted : t.text);
    return Semantics(
      button: true,
      enabled: !future,
      label: calendarDayLabel(d),
      excludeSemantics: true,
      child: GestureDetector(
        onTap: future ? null : onTap,
        child: Opacity(
          opacity: future ? 0.45 : 1,
          child: Container(
            width: 38,
            height: 38,
            alignment: Alignment.center,
            decoration: BoxDecoration(color: today ? t.accent : null, shape: BoxShape.circle),
            child: Text(
              '${d.day}',
              style: tkText(size: 15, weight: today ? 600 : 400, lineHeight: 1.2, color: ink).copyWith(fontFeatures: const [FontFeature.tabularFigures()]),
            ),
          ),
        ),
      ),
    );
  }
}
