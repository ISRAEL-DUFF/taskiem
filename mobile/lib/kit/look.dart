// The parts of the pages away from the chat in their "Direction B" look
// (docs/spec/mobile-app-look.md): the top card a page opens with, small
// labels in an accent's colour, highlight tiles two across with a figure
// and a small chart, a day's line against a usual day's, the stages orders
// move through, a split bar, and the trend words beside a figure. None of
// them needs a goal set: each compares the business with itself or says
// where things stand. All stand still when the phone asks for less motion.

import 'dart:math' as math;
import 'dart:ui' as ui;

import 'package:flutter/widgets.dart';

import '../theme/look.dart';
import '../theme/theme.dart';
import 'icon.dart';
import 'kit.dart';
import 'tap_area.dart';

bool _still(BuildContext context) => MediaQuery.maybeDisableAnimationsOf(context) ?? false;

/// What a press opens, for a screen reader and a finger: the label, the
/// tap, and the press's give.
Widget _pressable({required Widget child, VoidCallback? onTap, String? label, double scale = 0.98}) {
  if (onTap == null) return Semantics(container: true, label: label, explicitChildNodes: true, child: child);
  return TapArea(
    child: Semantics(
      button: true,
      label: label,
      onTap: onTap,
      child: TkPress(onTap: onTap, scale: scale, child: child),
    ),
  );
}

/// The card a page opens with: the panel's colour, rounder than the rest,
/// and in the dark a faint lime light at its top left.
class TkTopCard extends StatelessWidget {
  const TkTopCard({super.key, required this.child, this.onTap, this.semanticsLabel, this.padding = const EdgeInsets.all(16)});
  final Widget child;
  final VoidCallback? onTap;
  final String? semanticsLabel;
  final EdgeInsets padding;

  @override
  Widget build(BuildContext context) {
    final look = TkLook.of(context);
    return _pressable(
      onTap: onTap,
      label: semanticsLabel,
      scale: 0.985,
      child: Container(
        padding: padding,
        decoration: BoxDecoration(
          color: look.glow.a == 0 ? look.card : null,
          borderRadius: BorderRadius.circular(24),
          gradient: look.glow.a == 0
              ? null
              : RadialGradient(center: const Alignment(-0.8, -1), radius: 1.2, colors: [Color.alphaBlend(look.glow, look.card), look.card], stops: const [0, 0.6]),
        ),
        child: child,
      ),
    );
  }
}

/// A small label in capitals in an accent's colour, its icon before it and
/// a few words at its end.
class TkEyebrow extends StatelessWidget {
  const TkEyebrow(this.label, {super.key, required this.color, this.icon, this.trailing});
  final String label;
  final Color color;
  final String? icon;
  final String? trailing;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Row(
      children: [
        if (icon != null) ...[ExcludeSemantics(child: TkIcon(icon!, size: 15, stroke: 2.4, color: color)), const SizedBox(width: 6)],
        // The label first: three parts of the room to the trailing words' two.
        Expanded(
          flex: 3,
          child: Text(label.toUpperCase(), semanticsLabel: label, maxLines: 2, overflow: TextOverflow.ellipsis, style: tkText(size: 11.5, weight: 700, lineHeight: 1.3, letterSpacing: 0.4, color: color)),
        ),
        if (trailing != null) ...[
          const SizedBox(width: 6),
          Flexible(
            flex: 2,
            child: Text(trailing!, maxLines: 1, overflow: TextOverflow.ellipsis, textAlign: TextAlign.end, style: tkText(size: 12, weight: 500, lineHeight: 1.3, color: t.muted)),
          ),
        ],
      ],
    );
  }
}

/// A figure large, as the top card and the tiles set it.
TextStyle tkFigure(BuildContext context, {double size = 40, Color? color}) =>
    tkDisplay(size: size, lineHeight: 1.1, letterSpacing: -size * 0.04, color: color ?? TkTheme.tokens(context).text).copyWith(fontFeatures: tkTabular);

/// Figures in columns of equal width (`font-variant-numeric: tabular-nums`),
/// with Inter's letter features kept.
const tkTabular = <FontFeature>[FontFeature.tabularFigures(), FontFeature('cv11'), FontFeature('ss01')];

/// A highlight: its label in an accent's colour, a figure, a line under it,
/// and a small chart if it has one; pressed, it opens its page.
class TkHighlight extends StatelessWidget {
  const TkHighlight({
    super.key,
    required this.label,
    required this.color,
    this.value,
    this.icon,
    this.trailing,
    this.line,
    this.chart,
    this.wide = false,
    this.beside = false,
    this.onTap,
    this.semanticsLabel,
  });
  final String label;

  /// On a wide tile, the chart beside the figure rather than under it.
  final bool beside;
  final Color color;
  final String? icon;
  final String? trailing;
  /// The figure; none for a tile that is all chart.
  final String? value;
  final String? line;

  /// Under the figure (a meter, a split bar), or beside it on a wide tile.
  final Widget? chart;

  /// The whole row's width.
  final bool wide;
  final VoidCallback? onTap;
  final String? semanticsLabel;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final figure = Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      mainAxisSize: MainAxisSize.min,
      children: [
        if (value != null)
          FittedBox(
            fit: BoxFit.scaleDown,
            alignment: Alignment.centerLeft,
            child: Text(value!, maxLines: 1, style: tkFigure(context, size: 20)),
          ),
        if (line != null && line!.isNotEmpty)
          Text(line!, maxLines: 2, overflow: TextOverflow.ellipsis, style: tkText(size: 12.5, lineHeight: 1.35, color: t.muted)),
      ],
    );
    return _pressable(
      onTap: onTap,
      label: semanticsLabel,
      scale: 0.97,
      child: Container(
        padding: const EdgeInsets.fromLTRB(14, 12, 14, 12),
        decoration: BoxDecoration(color: TkLook.of(context).card, borderRadius: BorderRadius.circular(20)),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            TkEyebrow(label, color: color, icon: icon, trailing: trailing),
            const SizedBox(height: 8),
            if (beside && chart != null)
              Row(
                crossAxisAlignment: CrossAxisAlignment.end,
                children: [
                  Flexible(child: figure),
                  const SizedBox(width: 12),
                  Expanded(child: chart!),
                ],
              )
            else ...[
              if (value != null || (line != null && line!.isNotEmpty)) figure,
              if (chart != null) ...[if (value != null) const SizedBox(height: 10) else const SizedBox(height: 4), chart!],
            ],
          ],
        ),
      ),
    );
  }
}

/// Highlights two across; a wide one takes its row.
class TkHighlights extends StatelessWidget {
  const TkHighlights({super.key, required this.tiles});
  final List<TkHighlight> tiles;

  @override
  Widget build(BuildContext context) {
    final rows = <List<TkHighlight>>[];
    for (final x in tiles) {
      if (!x.wide && rows.isNotEmpty && rows.last.length == 1 && !rows.last.first.wide) {
        rows.last.add(x);
      } else {
        rows.add([x]);
      }
    }
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        for (var r = 0; r < rows.length; r++) ...[
          if (r > 0) const SizedBox(height: 10),
          if (rows[r].first.wide)
            rows[r].first
          else
            IntrinsicHeight(
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Expanded(child: rows[r][0]),
                  const SizedBox(width: 10),
                  Expanded(child: rows[r].length > 1 ? rows[r][1] : const SizedBox()),
                ],
              ),
            ),
        ],
      ],
    );
  }
}

/// Up, down or about the same: the words after an arrow, in a soft pill.
class TkTrend extends StatelessWidget {
  const TkTrend(this.words, {super.key, required this.up, this.pill = true});
  final String words;

  /// True up, false down, null about the same.
  final bool? up;
  final bool pill;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final ink = switch (up) {
      true => look.tealInk,
      false => look.orangeInk,
      null => t.muted,
    };
    final style = tkText(size: 12.5, weight: 700, lineHeight: 1.35, color: ink);
    final text = Text.rich(
      TextSpan(
        children: [
          if (up != null)
            WidgetSpan(
              alignment: PlaceholderAlignment.middle,
              child: Padding(
                padding: const EdgeInsets.only(right: 4),
                child: RotatedBox(quarterTurns: up! ? 0 : 2, child: TkIcon('b-up', size: 13, fill: true, color: ink)),
              ),
            ),
          TextSpan(text: words),
        ],
      ),
      maxLines: 2,
      overflow: TextOverflow.ellipsis,
      style: style,
    );
    if (!pill) return text;
    return Align(
      alignment: Alignment.centerLeft,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 3),
        decoration: BoxDecoration(color: (up == null ? t.muted : ink).withValues(alpha: 0.13), borderRadius: BorderRadius.circular(999)),
        child: text,
      ),
    );
  }
}

/// A day's money as a line growing through the hours, over a usual day's,
/// dashed. [today] is the running total at each hour's start, from
/// midnight, up to [nowHour] (the hour of now, with its minutes as a
/// fraction); [usual] the same for a whole usual day (25 points), or null
/// when there is no history yet.
class TkDayLine extends StatelessWidget {
  const TkDayLine({super.key, required this.today, required this.nowHour, this.usual, required this.semanticsLabel});
  final List<double> today;
  final double nowHour;
  final List<double>? usual;
  final String semanticsLabel;

  /// The hours drawn: 6 am to 10 pm, wider when money came outside them.
  (int, int) _window() {
    var from = 6, to = 22;
    bool moved(List<double> xs, int i) => i > 0 && i < xs.length && xs[i] != xs[i - 1];
    for (final xs in [today, ?usual]) {
      for (var i = 1; i < xs.length; i++) {
        if (moved(xs, i)) {
          from = math.min(from, i - 1);
          to = math.max(to, math.min(24, i));
        }
      }
    }
    return (from, to);
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final (from, to) = _window();
    String h(int x) => x == 0 || x == 24 ? '12 am' : (x == 12 ? 'noon' : (x < 12 ? '$x am' : '${x - 12} pm'));
    final marks = [from, from + (to - from) ~/ 3, from + 2 * (to - from) ~/ 3, to];
    return Semantics(
      label: semanticsLabel,
      excludeSemantics: true,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          SizedBox(
            height: 112,
            child: TweenAnimationBuilder<double>(
              tween: Tween(begin: _still(context) ? 1 : 0, end: 1),
              duration: _still(context) ? Duration.zero : const Duration(milliseconds: 900),
              curve: Curves.easeOutCubic,
              builder: (context, grow, _) => CustomPaint(
                painter: _DayLinePainter(
                  today: today,
                  usual: usual,
                  nowHour: nowHour,
                  from: from.toDouble(),
                  to: to.toDouble(),
                  grow: grow,
                  line: look.lime,
                  usualColour: t.muted.withValues(alpha: 0.6),
                  base: t.border,
                  card: t.panel,
                ),
              ),
            ),
          ),
          const SizedBox(height: 6),
          Row(
            mainAxisAlignment: MainAxisAlignment.spaceBetween,
            children: [for (final m in marks) Text(h(m), style: tkText(size: 11, lineHeight: 1.2, color: t.muted))],
          ),
        ],
      ),
    );
  }
}

class _DayLinePainter extends CustomPainter {
  _DayLinePainter({
    required this.today,
    required this.usual,
    required this.nowHour,
    required this.from,
    required this.to,
    required this.grow,
    required this.line,
    required this.usualColour,
    required this.base,
    required this.card,
  });
  final List<double> today;
  final List<double>? usual;
  final double nowHour, from, to, grow;
  final Color line, usualColour, base, card;

  @override
  void paint(Canvas canvas, Size size) {
    final top = [...today, ...?usual].fold<double>(0, math.max);
    final max = top <= 0 ? 1.0 : top * 1.12;
    const pad = 6.0;
    Offset at(double hour, double v) => Offset(
      (hour - from) / (to - from) * size.width,
      size.height - pad - (v / max) * (size.height - 2 * pad),
    );
    canvas.drawLine(Offset(0, size.height - 0.5), Offset(size.width, size.height - 0.5), Paint()..color = base);

    // A usual day: dashed, the whole window.
    final u = usual;
    if (u != null && u.length > 1) {
      final p = Paint()
        ..color = usualColour
        ..style = PaintingStyle.stroke
        ..strokeWidth = 1.8;
      final path = _smooth([for (var i = from.floor(); i <= to.ceil() && i < u.length; i++) at(i.toDouble(), u[i])]);
      for (final m in path.computeMetrics()) {
        for (var d = 0.0; d < m.length; d += 8) {
          canvas.drawPath(m.extractPath(d, math.min(d + 4, m.length)), p);
        }
      }
    }

    // Today: up to now, grown in from the left.
    final points = <Offset>[];
    final end = from + (nowHour.clamp(from, to) - from) * grow;
    for (var i = from.floor(); i <= end.floor() && i < today.length; i++) {
      points.add(at(i.toDouble(), today[i]));
    }
    if (today.isNotEmpty && end > points.length - 1 + from) {
      final i = end.floor().clamp(0, today.length - 1);
      final next = (i + 1 < today.length) ? today[i + 1] : today.last;
      points.add(at(end, today[i] + (next - today[i]) * (end - i)));
    }
    if (points.length < 2) return;
    final path = _smooth(points);
    final area = Path.from(path)
      ..lineTo(points.last.dx, size.height)
      ..lineTo(points.first.dx, size.height)
      ..close();
    canvas.drawPath(
      area,
      Paint()..shader = ui.Gradient.linear(Offset.zero, Offset(0, size.height), [line.withValues(alpha: 0.32), line.withValues(alpha: 0)]),
    );
    canvas.drawPath(
      path,
      Paint()
        ..color = line
        ..style = PaintingStyle.stroke
        ..strokeWidth = 2.8
        ..strokeCap = StrokeCap.round,
    );
    final last = points.last;
    final guide = Paint()
      ..color = line.withValues(alpha: 0.5)
      ..strokeWidth = 1;
    for (var y = last.dy + 6; y < size.height; y += 5) {
      canvas.drawLine(Offset(last.dx, y), Offset(last.dx, math.min(y + 2, size.height)), guide);
    }
    canvas.drawCircle(last, 6.5, Paint()..color = card);
    canvas.drawCircle(last, 4.5, Paint()..color = line);
  }

  /// A line through the points, eased between them (a monotone curve, so
  /// a running total never seems to go down).
  static Path _smooth(List<Offset> p) {
    final path = Path()..moveTo(p.first.dx, p.first.dy);
    for (var i = 1; i < p.length; i++) {
      final a = p[i - 1], b = p[i];
      final mx = (a.dx + b.dx) / 2;
      path.cubicTo(mx, a.dy, mx, b.dy, b.dx, b.dy);
    }
    return path;
  }

  @override
  bool shouldRepaint(_DayLinePainter old) =>
      old.grow != grow || old.today != today || old.usual != usual || old.nowHour != nowHour || old.line != line || old.card != card;
}

/// The key under a chart: a short stroke of each line's colour, dashed for
/// the usual one, and its words.
class TkChartKey extends StatelessWidget {
  const TkChartKey({super.key, required this.items});
  final List<({String words, Color color, bool dashed})> items;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return ExcludeSemantics(
      child: Wrap(
        spacing: 14,
        runSpacing: 4,
        children: [
          for (final x in items)
            Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                SizedBox(
                  width: 14,
                  height: 3,
                  child: x.dashed
                      ? Row(children: [for (var i = 0; i < 2; i++) ...[if (i > 0) const SizedBox(width: 3), Container(width: 5, color: x.color)]])
                      : DecoratedBox(decoration: BoxDecoration(color: x.color, borderRadius: BorderRadius.circular(2))),
                ),
                const SizedBox(width: 6),
                Flexible(child: Text(x.words, style: tkText(size: 12, lineHeight: 1.3, color: t.muted))),
              ],
            ),
        ],
      ),
    );
  }
}

/// A stage orders move through: its word, how many are in it, its colour.
typedef TkStage = ({String label, int count, Color ink, Color dot});

/// The stages side by side, an arrow between each, the count large in the
/// stage's colour and a dot for each order (up to six).
class TkFlow extends StatelessWidget {
  const TkFlow({super.key, required this.stages});
  final List<TkStage> stages;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Semantics(
      container: true,
      label: stages.map((s) => '${s.label}: ${s.count}').join(', '),
      excludeSemantics: true,
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          for (var i = 0; i < stages.length; i++) ...[
            if (i > 0)
              Padding(
                padding: const EdgeInsets.only(top: 12),
                child: TkIcon('chevron', size: 14, color: t.muted.withValues(alpha: 0.6)),
              ),
            Expanded(
              child: Column(
                children: [
                  FittedBox(
                    fit: BoxFit.scaleDown,
                    child: Text('${stages[i].count}', maxLines: 1, style: tkFigure(context, size: 30, color: stages[i].count == 0 ? t.muted : stages[i].ink)),
                  ),
                  Text(stages[i].label, textAlign: TextAlign.center, maxLines: 2, overflow: TextOverflow.ellipsis, style: tkText(size: 12, weight: 600, lineHeight: 1.25, color: t.muted)),
                  const SizedBox(height: 6),
                  SizedBox(
                    height: 7,
                    child: Row(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        for (var d = 0; d < math.min(stages[i].count, 6); d++) ...[
                          if (d > 0) const SizedBox(width: 3),
                          Container(width: 7, height: 7, decoration: BoxDecoration(color: stages[i].dot, shape: BoxShape.circle)),
                        ],
                      ],
                    ),
                  ),
                ],
              ),
            ),
          ],
        ],
      ),
    );
  }
}

/// A whole cut into shares: one bar, a gap between each share, and the key
/// under it.
class TkSplitBar extends StatelessWidget {
  const TkSplitBar({super.key, required this.parts, this.percent = true});
  final List<({String label, int share, Color color})> parts;

  /// Each share's percent after its label in the key (false: the label
  /// says it, as an amount).
  final bool percent;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final shown = parts.where((p) => p.share > 0).toList();
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        ExcludeSemantics(
          child: SizedBox(
            height: 12,
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                for (var i = 0; i < shown.length; i++) ...[
                  if (i > 0) const SizedBox(width: 3),
                  Expanded(
                    flex: math.max(1, shown[i].share),
                    child: DecoratedBox(decoration: BoxDecoration(color: shown[i].color, borderRadius: BorderRadius.circular(4))),
                  ),
                ],
              ],
            ),
          ),
        ),
        const SizedBox(height: 10),
        Wrap(
          spacing: 12,
          runSpacing: 4,
          children: [
            for (final p in shown)
              MergeSemantics(
                child: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Container(width: 8, height: 8, decoration: BoxDecoration(color: p.color, shape: BoxShape.circle)),
                    const SizedBox(width: 5),
                    Flexible(child: Text(percent ? '${p.label} ${p.share}%' : p.label, style: tkText(size: 12, weight: 500, lineHeight: 1.3, color: t.muted))),
                  ],
                ),
              ),
          ],
        ),
      ],
    );
  }
}

/// Initials on a warm gradient, as the head shows the person.
class TkFace extends StatelessWidget {
  const TkFace(this.initials, {super.key, this.size = 40, this.colors});
  final String initials;
  final double size;

  /// The gradient (a name's own, [tkFaceFor]); else a warm one.
  final List<Color>? colors;

  @override
  Widget build(BuildContext context) => ExcludeSemantics(
    child: Container(
      width: size,
      height: size,
      alignment: Alignment.center,
      decoration: BoxDecoration(
        shape: BoxShape.circle,
        gradient: LinearGradient(begin: Alignment.topLeft, end: Alignment.bottomRight, colors: colors ?? const [Color(0xFFFFB199), Color(0xFFE8634A)]),
      ),
      child: Text(initials, maxLines: 1, textScaler: TextScaler.noScaling, style: tkText(size: size * 0.34, weight: 700, color: const Color(0xFFFFFFFF))),
    ),
  );
}

/// Small bars, one a stretch (an hour), the one picked out full and the
/// rest soft: a tile's chart.
class TkMiniBars extends StatelessWidget {
  const TkMiniBars({super.key, required this.values, required this.color, this.picked, this.height = 46, this.labels});
  final List<double> values;

  /// A word under each bar (the days of a week), the picked one bold.
  final List<String>? labels;
  final Color color;
  final int? picked;
  final double height;

  @override
  Widget build(BuildContext context) {
    final top = values.fold<double>(0, math.max);
    final t = TkTheme.tokens(context);
    final bars = ExcludeSemantics(
      child: SizedBox(
        height: height,
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.end,
          children: [
            for (var i = 0; i < values.length; i++) ...[
              if (i > 0) const SizedBox(width: 4),
              Expanded(
                child: Container(
                  height: top <= 0 ? 3 : math.max(3, height * values[i] / top),
                  decoration: BoxDecoration(
                    color: i == picked ? color : color.withValues(alpha: 0.35),
                    borderRadius: const BorderRadius.vertical(top: Radius.circular(3), bottom: Radius.circular(1)),
                  ),
                ),
              ),
            ],
          ],
        ),
      ),
    );
    final l = labels;
    if (l == null) return bars;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        bars,
        const SizedBox(height: 4),
        ExcludeSemantics(
          child: Row(
            children: [
              for (var i = 0; i < l.length; i++) ...[
                if (i > 0) const SizedBox(width: 4),
                Expanded(
                  child: Text(
                    l[i],
                    textAlign: TextAlign.center,
                    maxLines: 1,
                    overflow: TextOverflow.clip,
                    style: tkText(size: 10.5, weight: 600, color: i == picked ? t.text : t.muted),
                  ),
                ),
              ],
            ],
          ),
        ),
      ],
    );
  }
}

/// A soft band in an accent's colour inside a top card: what to see to
/// next (payments to match, the order that has waited longest, the next
/// bill), a leading tile, two lines, and its button.
class TkCallout extends StatelessWidget {
  const TkCallout({super.key, required this.leading, required this.title, this.sub, required this.color, this.button, this.semantics, this.onPressed});
  final Widget leading;
  final String title;
  final String? sub;
  final Color color;
  final String? button;
  final String? semantics;
  final VoidCallback? onPressed;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final large = _large(context);
    final press = button != null && onPressed != null ? TkSmallButton(label: button!, semantics: semantics, primary: true, onPressed: onPressed) : null;
    final words = Row(
      children: [
        leading,
        const SizedBox(width: 10),
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(title, maxLines: large ? 4 : 2, overflow: TextOverflow.ellipsis, style: tkText(size: 14, weight: 600, lineHeight: 1.3, color: t.text)),
              if (sub != null && sub!.isNotEmpty)
                Text(sub!, maxLines: large ? 6 : 2, overflow: TextOverflow.ellipsis, style: tkText(size: 12.5, lineHeight: 1.3, color: t.muted)),
            ],
          ),
        ),
        if (press != null && !large) ...[const SizedBox(width: 8), press],
      ],
    );
    return Container(
      padding: const EdgeInsets.fromLTRB(10, 8, 8, 8),
      decoration: BoxDecoration(color: color.withValues(alpha: 0.12), borderRadius: BorderRadius.circular(16)),
      // With large text, the button under the words.
      child: press != null && large
          ? Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [words, const SizedBox(height: 8), press])
          : words,
    );
  }
}

/// An icon white on a small square of an accent, as a row's or a
/// callout's tile.
class TkSquare extends StatelessWidget {
  const TkSquare(this.icon, {super.key, required this.color, this.size = 34});
  final String icon;
  final Color color;
  final double size;

  @override
  Widget build(BuildContext context) => ExcludeSemantics(
    child: Container(
      width: size,
      height: size,
      decoration: BoxDecoration(color: color, borderRadius: BorderRadius.circular(size * 0.32)),
      alignment: Alignment.center,
      child: TkIcon(icon, size: size * 0.55, stroke: 2.2, color: const Color(0xFFFFFFFF)),
    ),
  );
}

/// A bar of how much of a whole is done (an invoice paid), in an accent.
class TkProgress extends StatelessWidget {
  const TkProgress(this.value, {super.key, required this.color, required this.semanticsLabel});
  final double value;
  final Color color;
  final String semanticsLabel;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Semantics(
      label: semanticsLabel,
      excludeSemantics: true,
      child: ClipRRect(
        borderRadius: BorderRadius.circular(4),
        child: SizedBox(
          height: 8,
          child: Stack(
            children: [
              Container(color: t.tkSoft),
              TweenAnimationBuilder<double>(
                tween: Tween(begin: _still(context) ? value : 0, end: value.clamp(0.0, 1.0)),
                duration: _still(context) ? Duration.zero : const Duration(milliseconds: 700),
                curve: Curves.easeOutCubic,
                builder: (context, w, _) => FractionallySizedBox(
                  widthFactor: w,
                  heightFactor: 1,
                  child: DecoratedBox(decoration: BoxDecoration(color: color, borderRadius: BorderRadius.circular(4))),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

/// The steps something goes through, side by side on a line: each a dot
/// (filled once done), its word, and when.
class TkSteps extends StatelessWidget {
  const TkSteps({super.key, required this.steps});
  final List<({String label, String when, bool done})> steps;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    Widget dot(bool done) => Container(
      width: 14,
      height: 14,
      decoration: BoxDecoration(shape: BoxShape.circle, color: done ? look.lime : look.card, border: done ? null : Border.all(color: t.muted, width: 2)),
    );
    final label = steps.map((s) => '${s.label}${s.done ? (s.when.isEmpty ? ', done' : ', ${s.when}') : ', not yet'}').join('; ');
    // With large text, the steps down the card: across, their words break.
    if (_large(context)) {
      return Semantics(
        container: true,
        label: label,
        excludeSemantics: true,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            for (final s in steps)
              Padding(
                padding: const EdgeInsets.symmetric(vertical: 3),
                child: Row(
                  children: [
                    dot(s.done),
                    const SizedBox(width: 10),
                    Expanded(child: Text(s.label, style: tkText(size: 11.5, weight: 600, lineHeight: 1.25, color: t.text))),
                    if (s.when.isNotEmpty) Text(s.when, style: tkText(size: 10.5, lineHeight: 1.25, color: t.muted)),
                  ],
                ),
              ),
          ],
        ),
      );
    }
    return Semantics(
      container: true,
      label: label,
      excludeSemantics: true,
      child: Stack(
        children: [
          Positioned(
            left: 0,
            right: 0,
            top: 6,
            child: FractionallySizedBox(widthFactor: 1 - 1 / steps.length, child: Container(height: 2, color: look.line)),
          ),
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              for (final s in steps)
                Expanded(
                  child: Column(
                    children: [
                      Container(
                        width: 14,
                        height: 14,
                        decoration: BoxDecoration(
                          shape: BoxShape.circle,
                          color: s.done ? look.lime : look.card,
                          border: s.done ? null : Border.all(color: t.muted, width: 2),
                        ),
                      ),
                      const SizedBox(height: 4),
                      Text(s.label, textAlign: TextAlign.center, maxLines: 2, style: tkText(size: 11.5, weight: 600, lineHeight: 1.2, color: t.text)),
                      if (s.when.isNotEmpty) Text(s.when, textAlign: TextAlign.center, maxLines: 2, style: tkText(size: 10.5, lineHeight: 1.2, color: t.muted)),
                    ],
                  ),
                ),
            ],
          ),
        ],
      ),
    );
  }
}

/// A button of a person's card (Call, WhatsApp, Map): its icon over its
/// word, on the ground's colour.
typedef TkCardAction = ({String icon, String label, String? semantics, VoidCallback onTap});

/// A person or a business on one card: their letters on their face, their
/// name and a line under it, what is at its end, and the buttons to reach
/// them across its foot.
class TkPersonCard extends StatelessWidget {
  const TkPersonCard({super.key, required this.name, this.sub = '', this.actions = const [], this.trailing, this.children = const []});
  final String name;
  final String sub;
  final List<TkCardAction> actions;
  final Widget? trailing;

  /// More under the head (lines of detail, a button).
  final List<Widget> children;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    final ink = look.on;
    return Container(
      padding: const EdgeInsets.fromLTRB(14, 12, 14, 12),
      decoration: BoxDecoration(color: look.card, borderRadius: BorderRadius.circular(20)),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              TkFace(_letters(name), size: 40, colors: tkFaceFor(name)),
              const SizedBox(width: 12),
              Expanded(
                child: MergeSemantics(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(name, maxLines: 2, overflow: TextOverflow.ellipsis, style: tkText(size: 16, weight: 600, lineHeight: 1.3, color: t.text)),
                      if (sub.isNotEmpty) Text(sub, maxLines: 3, overflow: TextOverflow.ellipsis, style: tkText(size: 12.5, lineHeight: 1.35, color: t.muted)),
                    ],
                  ),
                ),
              ),
              if (trailing != null) ...[const SizedBox(width: 8), trailing!],
            ],
          ),
          if (actions.isNotEmpty) ...[
            const SizedBox(height: 12),
            Row(
              children: [
                for (var i = 0; i < actions.length; i++) ...[
                  if (i > 0) const SizedBox(width: 8),
                  Expanded(
                    child: TapArea(
                      child: Semantics(
                        button: true,
                        label: actions[i].semantics ?? actions[i].label,
                        excludeSemantics: true,
                        onTap: actions[i].onTap,
                        child: TkPress(
                          onTap: actions[i].onTap,
                          child: Container(
                            padding: const EdgeInsets.fromLTRB(4, 10, 4, 8),
                            decoration: BoxDecoration(color: look.ground, borderRadius: BorderRadius.circular(14)),
                            child: Column(
                              children: [
                                TkIcon(actions[i].icon, size: 20, stroke: 2, color: ink),
                                const SizedBox(height: 4),
                                Text(actions[i].label, maxLines: 1, overflow: TextOverflow.ellipsis, style: tkText(size: 12, weight: 600, color: ink)),
                              ],
                            ),
                          ),
                        ),
                      ),
                    ),
                  ),
                ],
              ],
            ),
          ],
          ...children,
        ],
      ),
    );
  }
}

String _letters(String name) {
  final words = name.split(RegExp(r'[\s·]+')).where((w) => w.isNotEmpty && RegExp(r'[A-Za-z0-9]').hasMatch(w[0])).toList();
  if (words.isEmpty) return '?';
  if (words.length == 1) return words.first.substring(0, math.min(2, words.first.length)).toUpperCase();
  return (words[0][0] + words[1][0]).toUpperCase();
}

/// One line of what was ordered: the item (and a line under it), its amount.
typedef TkBillLine = ({String name, String sub, Widget? under, String amount});

/// What was ordered on a card: each line with its amount, a hairline, the
/// total large.
class TkBill extends StatelessWidget {
  const TkBill({super.key, required this.lines, this.totalLabel = 'Total', required this.total});
  final List<TkBillLine> lines;
  final String totalLabel;
  final String total;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    return Container(
      padding: const EdgeInsets.fromLTRB(16, 6, 16, 12),
      decoration: BoxDecoration(color: look.card, borderRadius: BorderRadius.circular(20)),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          for (final l in lines)
            Padding(
              padding: const EdgeInsets.symmetric(vertical: 7),
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        MergeSemantics(
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              Text(l.name, style: tkText(size: 15, weight: 500, lineHeight: 1.35, color: t.text)),
                              if (l.sub.isNotEmpty) Text(l.sub, style: tkText(size: 12.5, lineHeight: 1.35, color: t.muted)),
                            ],
                          ),
                        ),
                        ?l.under,
                      ],
                    ),
                  ),
                  const SizedBox(width: 12),
                  Text(l.amount, style: tkText(size: 15, weight: 600, lineHeight: 1.35, color: t.text).copyWith(fontFeatures: tkTabular)),
                ],
              ),
            ),
          Container(height: 1, margin: const EdgeInsets.symmetric(vertical: 6), color: look.line),
          MergeSemantics(
            child: Row(
              children: [
                Expanded(child: Text(totalLabel, style: tkText(size: 15, weight: 700, color: t.text))),
                Text(total, style: tkDisplay(size: 19, letterSpacing: -0.4, color: t.text).copyWith(fontFeatures: tkTabular)),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

/// A small tile inside a top card: an icon, a figure, a line under it.
class TkMiniTile extends StatelessWidget {
  const TkMiniTile({super.key, required this.icon, required this.value, required this.sub});
  final String icon;
  final String value;
  final String sub;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final look = TkLook.of(context);
    return MergeSemantics(
      child: Container(
        padding: const EdgeInsets.fromLTRB(12, 10, 12, 10),
        decoration: BoxDecoration(color: look.ground, borderRadius: BorderRadius.circular(14)),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            ExcludeSemantics(child: TkIcon(icon, size: 18, stroke: 2, color: t.muted)),
            const SizedBox(height: 4),
            Text(value, maxLines: 2, overflow: TextOverflow.ellipsis, style: tkText(size: 15, weight: 650, lineHeight: 1.3, color: t.text)),
            Text(sub, maxLines: 2, overflow: TextOverflow.ellipsis, style: tkText(size: 12, lineHeight: 1.3, color: t.muted)),
          ],
        ),
      ),
    );
  }
}

/// The buttons of a top card, side by side, sharing its width.
class TkButtonRow extends StatelessWidget {
  const TkButtonRow(this.buttons, {super.key});
  final List<Widget> buttons;

  @override
  Widget build(BuildContext context) => _large(context)
      // With large text, one under another: side by side, the words break.
      ? Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            for (var i = 0; i < buttons.length; i++) ...[if (i > 0) const SizedBox(height: 8), buttons[i]],
          ],
        )
      : Row(
          children: [
            for (var i = 0; i < buttons.length; i++) ...[if (i > 0) const SizedBox(width: 8), Expanded(child: buttons[i])],
          ],
        );
}

/// The phone asks for text half as large again or more.
bool _large(BuildContext context) => MediaQuery.textScalerOf(context).scale(10) > 15;
