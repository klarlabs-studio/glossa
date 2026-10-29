/// The SPEC §6 surfaces through the Flutter layer: `explain()` field for
/// field, and the error channel.
library;

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:glossa_flutter/glossa_flutter.dart';

import 'glossa_text_test.dart' show offlineClient, pump, rendered;

void main() {
  testWidgets('explain() is the SPEC §6 document, field for field', (
    tester,
  ) async {
    final client = await offlineClient(tester);
    addTearDown(client.dispose);
    late Glossa glossa;
    await pump(
      tester,
      client,
      Builder(
        builder: (context) {
          glossa = GlossaScope.of(context);
          return const SizedBox.shrink();
        },
      ),
    );

    expect(glossa.explain('cart.checkout').toJson(), {
      'id': 'cart.checkout',
      'requested': ['de-AT'],
      'locale': 'de-AT',
      'chain': ['de-AT', 'de', 'en'],
      'resolvedFrom': 'de',
      'release': {'id': 'rel_1', 'version': 1},
      'source': 'bundled',
      'steps': [
        {'locale': 'de-AT', 'outcome': 'missing'},
        {'locale': 'de', 'outcome': 'found'},
      ],
      // No staged rollout in this release (SPEC §1.4, §6).
      'rollout': null,
    });

    // Only `de` carries cart.hint, so `en` never answers; and a message
    // nothing has resolves inline, with resolvedFrom null.
    expect(glossa.explain('cart.nowhere').toJson(), {
      'id': 'cart.nowhere',
      'requested': ['de-AT'],
      'locale': 'de-AT',
      'chain': ['de-AT', 'de', 'en'],
      'resolvedFrom': null,
      'release': {'id': 'rel_1', 'version': 1},
      'source': 'inline',
      'steps': [
        {'locale': 'de-AT', 'outcome': 'missing'},
        {'locale': 'de', 'outcome': 'missing'},
        {'locale': 'en', 'outcome': 'missing'},
      ],
      'rollout': null,
    });
  });

  testWidgets('explain() takes other locales without switching', (
    tester,
  ) async {
    final client = await offlineClient(tester);
    addTearDown(client.dispose);
    late Glossa glossa;
    await pump(
      tester,
      client,
      Builder(
        builder: (context) {
          glossa = GlossaScope.of(context);
          return const GlossaText('cart.checkout');
        },
      ),
    );

    expect(glossa.explain('cart.checkout', ['en']).resolvedFrom, 'en');
    expect(glossa.locale, 'de-AT', reason: 'explain has no side effects');
    expect(rendered(tester), 'Zur Kasse');
  });

  testWidgets('the error channel reaches the application', (tester) async {
    final client = await offlineClient(tester);
    addTearDown(client.dispose);
    final seen = <GlossaError>[];

    await pump(
      tester,
      client,
      const GlossaText('cart.nowhere'),
      onError: seen.add,
    );

    expect(seen, hasLength(1));
    expect(seen.single.type, ErrorType.missingMessage);
    expect(seen.single.messageId, 'cart.nowhere');
    expect(seen.single.toJson(), {
      'type': 'missing-message',
      'detail': 'no locale in the chain has the message',
      'messageId': 'cart.nowhere',
      'locale': 'de-AT',
      'releaseId': 'rel_1',
    });
    // Nothing threw: the widget still rendered.
    expect(rendered(tester), 'cart.nowhere');
  });

  testWidgets('the scope exposes the release, source and direction', (
    tester,
  ) async {
    final client = await offlineClient(tester, locales: ['ar']);
    addTearDown(client.dispose);
    late Glossa glossa;
    await pump(
      tester,
      client,
      Builder(
        builder: (context) {
          glossa = GlossaScope.of(context);
          return const SizedBox.shrink();
        },
      ),
    );

    expect(glossa.locale, 'ar');
    expect(glossa.textDirection, TextDirection.rtl);
    expect(glossa.release?.id, 'rel_1');
    expect(glossa.source, Source.bundled);
    expect(glossa.availableLocales.map((l) => l.code), [
      'de',
      'en',
      'ar',
      'de-AT',
    ]);
  });

  testWidgets('setLocales rebuilds the subtree once the chain has loaded', (
    tester,
  ) async {
    final client = await offlineClient(tester);
    addTearDown(client.dispose);
    late Glossa glossa;
    await pump(
      tester,
      client,
      Builder(
        builder: (context) {
          glossa = GlossaScope.of(context);
          return const GlossaText('cart.checkout');
        },
      ),
    );
    expect(rendered(tester), 'Zur Kasse');

    await tester.runAsync(() => glossa.setLocales(['en']));
    await tester.pump();

    expect(rendered(tester), 'Checkout');
  });

  testWidgets('the scope does not dispose the client it was given', (
    tester,
  ) async {
    final client = await offlineClient(tester);
    addTearDown(client.dispose);
    await pump(tester, client, const GlossaText('cart.checkout'));
    await tester.pumpWidget(const SizedBox.shrink());

    // The client still renders and its error channel is still open,
    // because the application owns its lifetime, not the widget tree.
    expect(client.t('cart.checkout'), 'Zur Kasse');
    expect(client.errors.isBroadcast, isTrue);
  });
}
