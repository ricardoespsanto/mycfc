# Política de cookies e armazenamento local

Versão proposta: `2026-09-04`
Entrada em vigor: **[a deliberar]**

## O que o MyCFC utiliza

O MyCFC utiliza apenas mecanismos estritamente necessários para prestar e proteger o serviço, designadamente:

| Mecanismo | Finalidade | Duração proposta |
|---|---|---|
| `mycfc_session` | Manter sessão, mensagens temporárias e proteção dos fluxos; `HttpOnly`, `SameSite=Lax` e `Secure` em produção | Máximo configurado de 12 horas e inatividade de 30 minutos; pode ser eliminado antes no logout/revogação |
| Token de proteção CSRF associado à sessão/formulário | Impedir pedidos forjados e proteger formulários | Sessão ou período técnico equivalente |
| `sessionStorage` do cartão de treino | Recordar, apenas no separador atual, se o cartão foi aberto ou fechado | Até fechar o separador/janela |

O formulário de registo carrega **Cloudflare Turnstile** para prevenir submissões automatizadas e transmite à Cloudflare o token de desafio e o endereço IP. A Cloudflare pode usar armazenamento estritamente necessário ao desafio segundo a sua documentação. A Direção deve confirmar contrato, configuração, duração e informação internacional antes da publicação.

## Sem medição ou publicidade opcional

O código da versão abrangida por esta proposta não inclui cookies de publicidade, criação de perfis, medição de audiência opcional ou marketing. A configuração efetivamente implantada de Cloudflare/Caddy e o comportamento do Turnstile têm de ser verificados antes de a Direção declarar o inventário completo. Confirmando-se apenas mecanismos necessários, não se apresenta um banner de «aceitar cookies» que nada acrescentaria ao controlo do utilizador.

Ligações para websites, redes sociais, WhatsApp, federações ou fornecedores externos não depositam, por si só, cookies desses terceiros no MyCFC. Ao seguir a ligação, aplica-se a política do destino.

## Controlo e alterações

O navegador permite apagar ou bloquear armazenamento. Bloquear os mecanismos estritamente necessários pode impedir o início de sessão ou a submissão segura de formulários.

Antes de introduzir análise, marketing, vídeo incorporado, mapa ou outro serviço opcional, o CFC atualizará o inventário e esta política e, quando exigido, disponibilizará rejeição, aceitação e preferências com igual facilidade, sem carregar o serviço antes da escolha.

Questões podem ser enviadas pelo canal indicado em «Exercer direitos».
