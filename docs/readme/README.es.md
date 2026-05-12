# nexus-trade-bot

<p align="center">
  <img src="../../logo/logo.png" alt="Nexus Trade Bot" width="720">
</p>

**Un centro de control de robots en red creado para comerciantes que desean volumen, automatización y visibilidad de riesgos sin tener que cuidar cada pedido. Los futuros son el modo predeterminado; Las redes spot son compatibles con los principales intercambios centralizados.**

[![Go](https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-green)](../../LICENSE)
[![One Command](https://img.shields.io/badge/install-one%20command-blue)](#one-command-install)
[![Languages](https://img.shields.io/badge/languages-11-orange)](#languages)

## Únete a la comunidad

Las dudas de despliegue, los detalles de las APIs de exchanges, la experiencia en trading real y los comentarios de nuevas versiones se resuelven mejor en un solo lugar. Únete al grupo de usuarios de Nexus Trade Bot: [https://t.me/nexustradebot8](https://t.me/nexustradebot8).

## Idiomas

[English](../../README.md) | [简体中文](README.zh-CN.md) | [Русский](README.ru.md) | [한국어](README.ko.md) | [日本語](README.ja.md) | Español | [Tiếng Việt](README.vi.md) | [हिन्दी](README.hi.md) | [Português](README.pt.md) | [العربية](README.ar.md) | [繁體中文](README.zh-TW.md)


## Instalación con un solo comando

Ejecute esto en un servidor Ubuntu nuevo:

```bash
wget -O nexus-trade-bot.sh https://raw.githubusercontent.com/haohaoi34/nexus-trade-bot/main/scripts/nexus-trade-bot.sh && chmod +x nexus-trade-bot.sh && ./nexus-trade-bot.sh install && ./nexus-trade-bot.sh start
```

El servidor ejecuta automáticamente:

- Instala las dependencias faltantes de Ubuntu.
- Instala Go si el servidor no tiene una versión compatible.
- Clona `https://github.com/haohaoi34/nexus-trade-bot.git` cuando aún no se está ejecutando dentro de un pago de origen.
- Compila el bot desde el código fuente o utiliza el binario incluido en un paquete de lanzamiento.
- Crea `config.yaml` a partir de `config.example.yaml` si es necesario y lo mantiene local.
- Inicia la consola web en segundo plano y escribe registros en `logs/`.
- Detecta automáticamente la IP pública del servidor y muestra un bloque de acceso claro con la URL local, la URL del servidor, el archivo PID y la ruta del log.

Comandos útiles del servidor:

```bash
./nexus-trade-bot.sh install
./nexus-trade-bot.sh start
./nexus-trade-bot.sh status
./nexus-trade-bot.sh logs
./nexus-trade-bot.sh restart
./nexus-trade-bot.sh stop
./nexus-trade-bot.sh update
```

Inicio de sesión web predeterminado:

```text
username: admin
password: admin
```

Cambie la contraseña predeterminada inmediatamente después de su primer inicio de sesión.


## Intercambios admitidos

| Intercambio | Soporte |
| --- | --- |
| Binance | Futuros: estable. Lugar: estable. Lo mejor para redes al contado y perpetuas USDT/USDC de alta liquidez. |
| Bitget | Futuros: estable. Lugar: estable. Lo mejor para el comercio en red y las estrategias de volumen de reembolso de tarifas. |
| Gate.io | Futuros: estable. Lugar: estable. Útil para la diversificación de múltiples intercambios. |
| Bybit | Futuros: beta. Lugar: estable. Pruebe primero con un tamaño más pequeño. |
| OKX | Futuros: beta. Lugar: estable. Requiere clave API, clave secreta y frase de contraseña. |
| Hyperliquid | Futuros: beta. Lugar: beta. Utiliza configuración API basada en billetera y pares al contado USDC. |

Enlace de reembolso de Bitget: [hasta un 70% de reembolso de tarifa, código de invitación `4n9z`](https://partner.hdmune.cn/bg/3DLRKF).


## Qué hace

nexus-trade-bot le ayuda a ejecutar estrategias grid desde una consola web limpia:

- Agregue las API de Exchange una vez y verifíquelas antes de usarlas.
- Cree múltiples bots para diferentes símbolos, cuentas e instrucciones.
- Elija futuros o spot. Los futuros están seleccionados de forma predeterminada.
- Utilice el modo largo, corto o neutral en futuros; Utilice el modo largo en el acto.
- Cargue los símbolos spot de Binance, Bitget, Bybit, OKX, Gate e Hyperliquid automáticamente.
- Observe los saldos, el volumen de operaciones, el estado del bot y PnL en tiempo real.
- Pause un bot, cambie los parámetros y reinícielo con la configuración más reciente.
- Deje que el monitor de riesgos deje de operar durante movimientos anormales del mercado.

Está diseñado para traders que se preocupan por la ejecución, la facturación y el control, no para personas que quieren seguir editando archivos de configuración todo el día.

## La idea central

Un robot de red coloca órdenes de compra y venta a intervalos de precios fijos. En lugar de intentar predecir el máximo o el mínimo exacto, sigue trabajando en torno a un rango de precios:

- Cuando el precio baja, el robot compra gradualmente de acuerdo con la configuración de su red.
- Cuando el precio rebota, el robot vende niveles más altos paso a paso.
- En un mercado lateral o en recuperación alcista, esto puede convertir la volatilidad en operaciones realizadas repetidas.
- En una tendencia bajista unidireccional, el robot acumula posición y necesita suficiente margen, límites de riesgo y paciencia.

El objetivo no es un beneficio mágico. El objetivo es una ejecución disciplinada: espaciamiento de órdenes consistente, tamaño de orden controlado, riesgo visible y reacción automática cuando el mercado se vuelve anormal.


## Estrategia de ejemplo: red ETH con alta rotación

A continuación se muestra un ejemplo práctico para comprender cómo los comerciantes utilizan este tipo de bot.

Supongamos que ETH cotiza cerca de `3000` y usted configura:

| Parámetro | Ejemplo |
| --- | --- |
| Símbolo | `ETHUSDT` o `ETHUSDC` |
| Dirección | Rejilla larga |
| Intervalo de precios | `1 USDT` |
| Importe del pedido | `300 USDT` por orden de cuadrícula |
| Estilo de mercado | Mercado lateral o en recuperación alcista |

Con un intervalo `1 USDT` ajustado y liquidez ETH activa, el bot puede generar una rotación muy alta. En un mercado ajetreado, este tipo de configuración puede alcanzar millones de dólares en volumen de operaciones diario y decenas de millones en volumen mensual, dependiendo de la volatilidad, las tarifas, la liquidez y el tamaño de la cuenta.

Es por eso que muchos comerciantes utilizan sistemas grid con dos propósitos:

- **Creación de volumen**: aumentar el volumen de operaciones de futuros para campañas o niveles VIP de intercambio.
- **Recolección de volatilidad**: comprar repetidamente más bajo y vender más alto dentro de un rango.


## Ejemplo de lógica de reducción

El comercio en red debe planificarse en torno a la reducción.

Supongamos que ETH comienza cerca de `3000` y cae a `2700`. Una grilla larga generalmente tendrá una pérdida flotante porque ha comprado en el camino hacia abajo. Pero también ha acumulado entradas más bajas. Si el precio luego rebota de `2700` hacia `2850`, el costo promedio puede reducirse lo suficiente como para que la cuenta se acerque al punto de equilibrio antes que una sola entrada en `3000`.

Si ETH regresa cerca del área `3000` original, la estrategia puede beneficiarse de ambos:

- recuperación de inventarios tras el repunte;
- spreads de cuadrícula realizados recopilados durante el movimiento.

Algunos operadores reservan un margen de margen mayor, por ejemplo alrededor de `30,000 USDT`, para diseñar una cuadrícula que pueda tolerar un movimiento mucho más profundo, como una reducción de `1000 USDT` ETH. Que eso sea suficiente depende del apalancamiento, el modo de margen, el tamaño de la posición, las tarifas, las reglas de margen de mantenimiento del intercambio y qué tan agresiva sea su red.

El punto importante: las ganancias de la red provienen de la preparación, no del optimismo. Antes de correr el tamaño, calcule hasta dónde puede moverse el mercado en su contra, cuánta posición puede acumular el robot y qué sucede si el mercado no se recupera rápidamente.


## Protección contra riesgos incorporada

Las caídas rápidas en un sentido son el peor entorno para una red larga y agresiva. nexus-trade-bot incluye un monitor de riesgo de mercado diseñado para reducir este problema:

- observa los símbolos principales como BTC, ETH, SOL, XRP y DOGE;
- detecta comportamiento anormal de precios y volúmenes;
- detiene las operaciones cuando las condiciones del mercado se vuelven peligrosas;
- permite operar nuevamente solo después de que se recuperen suficientes símbolos monitoreados.

Esto no elimina el riesgo, pero le da al robot la oportunidad de dejar de agregar exposición durante movimientos repentinos de liquidación.


## Formas comunes de usarlo

### 1. Creación de volumen y niveles VIP

Utilice intervalos ajustados y tamaños de orden controlados en símbolos de gran liquidez. El objetivo es una alta rotación con una ejecución predecible. Las tarifas son muy importantes aquí, así que utilice pares de tarifas bajas o programas de reembolso siempre que sea posible.

### 2. Cuadrícula larga después de un retroceso del mercado

Comience después de una caída significativa en lugar de perseguir una bomba vertical. El robot compra por capas y vende por rebotes. Este estilo necesita suficiente margen para sobrevivir a retrocesos más profundos.

### 3. Cuadrícula al contado de Binance

Utilice el modo spot cuando desee que el bot compre y venda monedas reales en lugar de abrir posiciones de futuros apalancadas. El modo spot es solo largo: el robot compra primero los niveles más bajos y vende el inventario para obtener rebotes. Es más simple que los futuros, pero aún necesita suficiente saldo de cotización y un plan para tendencias bajistas prolongadas.

### 4. Salida de inventario

Si ya mantiene una posición, el robot puede ayudarle a venderla gradualmente a medida que sube el precio. Cuando la posición se reduce por completo, puedes detener el bot.

### 5. Cuadrícula neutra

Utilice el modo neutral cuando desee un comportamiento de cuadrícula tanto en el lado largo como en el lado corto. Comience con un tamaño más pequeño y observe cómo el intercambio maneja el modo de posición antes de escalar.


## Guía de parámetros

| Configuración | Lo que significa | Consejo práctico |
| --- | --- | --- |
| `symbol` | Par comercial | Comience con pares líquidos como BTC o ETH. |
| `app.market_type` | `futures` o `spot` | El valor predeterminado es `futures`. El comercio al contado en vivo admite Binance, Bitget, Bybit, OKX, Gate e Hyperliquid a través de adaptadores dedicados. |
| `direction` | `long`, `short` o `neutral` | Las cuadrículas largas necesitan margen para las reducciones. Las cuadrículas cortas no deben adoptar accidentalmente una posición corta manual no relacionada a menos que habilites ese comportamiento intencionalmente. |
| `price_interval` | Distancia entre niveles de cuadrícula | Un intervalo más pequeño significa más operaciones y más tarifas. |
| `order_quantity` | Cantidad utilizada por pedido | Una cantidad mayor aumenta la rotación y la reducción. Confirme si la interfaz de usuario muestra el valor de cotización o la cantidad base para su intercambio y tipo de mercado. |
| `min_order_value` | Pedido mínimo teórico | Debe satisfacer los mínimos de cambio. |
| `risk_control.enabled` | Protección contra anomalías del mercado | Mantenlo habilitado a menos que sepas exactamente por qué no. |


## Consola web

La consola admite 11 idiomas:

Inglés, chino simplificado, ruso, coreano, japonés, español, vietnamita, hindi, portugués, árabe y chino tradicional.

El modo Consola web muestra:

- Gestión de API
- creación y edición de bots
- intercambiar logotipos
- saldos en tiempo real
- PnL realizado hoy y total
- hoy y volumen total de operaciones
- estados del bot en ejecución, pausado y detenido


## Instalación manual

```bash
git clone https://github.com/haohaoi34/nexus-trade-bot.git
cd nexus-trade-bot
go mod download
go build -o nexus-trade-bot .
```

Inicie la consola web:

```bash
./nexus-trade-bot
```

URL local predeterminada:

```text
http://127.0.0.1:8080
```

Exponer en un servidor:

```bash
NEXUS_TRADE_BOT_ADDR=0.0.0.0:8080 ./nexus-trade-bot
```

Ejecutor de servidor de un solo comando desde un pago de origen:

```bash
chmod +x scripts/nexus-trade-bot.sh
scripts/nexus-trade-bot.sh install
scripts/nexus-trade-bot.sh start
scripts/nexus-trade-bot.sh status
scripts/nexus-trade-bot.sh logs
scripts/nexus-trade-bot.sh stop
```

El ejecutor funciona tanto desde un paquete fuente como desde un paquete de lanzamiento. En modo fuente construye `./nexus-trade-bot`; en el modo de lanzamiento, utiliza el binario incluido directamente.

Ejecute el modo de trabajo CLI:

```bash
./nexus-trade-bot worker config.yaml
```


## Antes de operar en vivo

Comprueba estos primero:

- La clave API tiene permiso comercial pero no permiso de retiro.
- El modo de margen es lo que esperas.
- El apalancamiento no es demasiado agresivo.
- El símbolo tiene suficiente liquidez.
- El tamaño del pedido cumple con los mínimos de cambio.
- Entiendes cuánta posición puede acumular la cuadrícula.
- Tiene un plan para mercados unidireccionales.
- El firewall de su servidor expone el puerto web sólo cuando está previsto.


## Descargo de responsabilidad

El comercio de futuros puede causar pérdidas significativas. Las estrategias grid pueden funcionar bien en mercados en recuperación o en un rango limitado, pero también pueden acumular grandes posiciones durante tendencias fuertes unidireccionales. nexus-trade-bot es un software de ejecución; usted es responsable de la configuración de la estrategia, la configuración del intercambio, el riesgo de la cuenta y cada operación realizada a través de sus claves API.
